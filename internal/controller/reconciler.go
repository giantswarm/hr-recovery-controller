package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	AnnotationRequestedAt = "reconcile.fluxcd.io/requestedAt"
	AnnotationForceAt     = "reconcile.fluxcd.io/forceAt"

	AnnotationLastPokeAt = "hr-recovery.giantswarm.io/last-poke-at"
	AnnotationPokeCount  = "hr-recovery.giantswarm.io/poke-count"
	AnnotationSkip       = "hr-recovery.giantswarm.io/skip"
)

// Reconciler watches HelmReleases and unwedges those stuck on
// Stalled=True / reason=MissingRollbackTarget by force-poking them.
type Reconciler struct {
	client.Client
	Recorder        record.EventRecorder
	MaxPokes        int
	Backoff         time.Duration
	NamespacePrefix string
	LabelSelector   labels.Selector

	// now is injectable for tests.
	now func() time.Time
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.now == nil {
		r.now = time.Now
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("hr-recovery").
		For(&helmv2.HelmRelease{}, builder.WithPredicates(stuckPredicate())).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("helmrelease", req.NamespacedName)

	var hr helmv2.HelmRelease
	if err := r.Get(ctx, req.NamespacedName, &hr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !r.inScope(&hr) {
		return ctrl.Result{}, nil
	}

	// On successful recovery (Ready=True with a non-zero poke-count), reset
	// the counter so a later independent stall gets a fresh budget.
	if isReady(&hr) {
		if c := hr.Annotations[AnnotationPokeCount]; c != "" && c != "0" {
			logger.Info("HR is Ready after recovery; resetting poke-count", "previousCount", c)
			if err := r.patchAnnotations(ctx, &hr, map[string]string{
				AnnotationPokeCount: "0",
			}); err != nil {
				return ctrl.Result{}, err
			}
			successesTotal.WithLabelValues(req.Namespace, req.Name).Inc()
		}
		return ctrl.Result{}, nil
	}

	if !isStuck(&hr) {
		return ctrl.Result{}, nil
	}

	if hr.Spec.Suspend {
		logger.Info("HR is suspended; skipping")
		return ctrl.Result{}, nil
	}
	if hr.Annotations[AnnotationSkip] == "true" {
		logger.Info("HR has skip annotation; skipping")
		return ctrl.Result{}, nil
	}

	if last, ok := parseUnixNano(hr.Annotations[AnnotationLastPokeAt]); ok {
		if elapsed := r.now().Sub(last); elapsed < r.Backoff {
			wait := r.Backoff - elapsed
			logger.Info("inside backoff window; requeueing", "wait", wait)
			return ctrl.Result{RequeueAfter: wait}, nil
		}
	}

	count, _ := strconv.Atoi(hr.Annotations[AnnotationPokeCount])
	if count >= r.MaxPokes {
		logger.Info("max pokes reached; giving up", "count", count, "max", r.MaxPokes)
		r.Recorder.Eventf(&hr, "Warning", "RecoveryGaveUp",
			"Gave up after %d pokes", count)
		giveupsTotal.WithLabelValues(req.Namespace, req.Name).Inc()
		return ctrl.Result{}, nil
	}

	token := strconv.FormatInt(r.now().UnixNano(), 10)
	next := count + 1
	if err := r.patchAnnotations(ctx, &hr, map[string]string{
		AnnotationRequestedAt: token,
		AnnotationForceAt:     token,
		AnnotationLastPokeAt:  token,
		AnnotationPokeCount:   strconv.Itoa(next),
	}); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("poked stuck HR", "count", next, "token", token)
	r.Recorder.Eventf(&hr, "Normal", "RecoveryPoke",
		"Poke %d (token %s)", next, token)
	pokesTotal.WithLabelValues(req.Namespace, req.Name).Inc()

	return ctrl.Result{RequeueAfter: r.Backoff}, nil
}

func (r *Reconciler) inScope(hr *helmv2.HelmRelease) bool {
	if r.NamespacePrefix != "" && !strings.HasPrefix(hr.Namespace, r.NamespacePrefix) {
		return false
	}
	if r.LabelSelector != nil && !r.LabelSelector.Matches(labels.Set(hr.Labels)) {
		return false
	}
	return true
}

// patchAnnotations applies a JSON-merge patch that only touches
// metadata.annotations, so no generation bump and no resource-version conflict
// loops on the spec.
func (r *Reconciler) patchAnnotations(ctx context.Context, hr *helmv2.HelmRelease, kv map[string]string) error {
	body, err := buildAnnotationPatch(kv)
	if err != nil {
		return err
	}
	patch := client.RawPatch(types.MergePatchType, body)
	target := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{Namespace: hr.Namespace, Name: hr.Name},
	}
	if err := r.Patch(ctx, target, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("patch annotations: %w", err)
	}
	return nil
}

func buildAnnotationPatch(kv map[string]string) ([]byte, error) {
	type meta struct {
		Annotations map[string]string `json:"annotations"`
	}
	return json.Marshal(struct {
		Metadata meta `json:"metadata"`
	}{Metadata: meta{Annotations: kv}})
}

func parseUnixNano(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
}
