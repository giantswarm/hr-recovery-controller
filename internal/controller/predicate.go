package controller

import (
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	stalledConditionType   = "Stalled"
	readyConditionType     = "Ready"
	missingRollbackTarget  = "MissingRollbackTarget"
)

func isStuck(obj client.Object) bool {
	hr, ok := obj.(*helmv2.HelmRelease)
	if !ok {
		return false
	}
	for _, c := range hr.Status.Conditions {
		if c.Type == stalledConditionType &&
			c.Status == metav1.ConditionTrue &&
			c.Reason == missingRollbackTarget {
			return true
		}
	}
	return false
}

func isReady(obj client.Object) bool {
	hr, ok := obj.(*helmv2.HelmRelease)
	if !ok {
		return false
	}
	for _, c := range hr.Status.Conditions {
		if c.Type == readyConditionType && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// stuckPredicate enqueues HRs that are currently stuck, plus HRs that were
// previously stuck and have just become Ready (so we can reset poke-count).
func stuckPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return isStuck(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isStuck(e.ObjectNew) || (isStuck(e.ObjectOld) && isReady(e.ObjectNew))
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}
