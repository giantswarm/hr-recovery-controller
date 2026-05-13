package controller

import (
	"context"
	"strconv"
	"testing"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := helmv2.AddToScheme(s); err != nil {
		t.Fatalf("add helmv2 scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
	}
	return s
}

func newReconciler(t *testing.T, now time.Time, objs ...client.Object) (*Reconciler, *record.FakeRecorder) {
	t.Helper()
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	rec := record.NewFakeRecorder(16)
	return &Reconciler{
		Client:   c,
		Recorder: rec,
		MaxPokes: 10,
		Backoff:  5 * time.Minute,
		now:      func() time.Time { return now },
	}, rec
}

func stuckHR(name, ns string, ann map[string]string) *helmv2.HelmRelease {
	return &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: ann,
		},
		Status: helmv2.HelmReleaseStatus{
			Conditions: []metav1.Condition{
				{
					Type:   stalledConditionType,
					Status: metav1.ConditionTrue,
					Reason: missingRollbackTarget,
				},
			},
		},
	}
}

func readyHR(name, ns string, ann map[string]string) *helmv2.HelmRelease {
	return &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: ann,
		},
		Status: helmv2.HelmReleaseStatus{
			Conditions: []metav1.Condition{
				{Type: readyConditionType, Status: metav1.ConditionTrue},
			},
		},
	}
}

func reconcile(t *testing.T, r *Reconciler, name, ns string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res
}

func getHR(t *testing.T, r *Reconciler, name, ns string) *helmv2.HelmRelease {
	t.Helper()
	var hr helmv2.HelmRelease
	if err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, &hr); err != nil {
		t.Fatalf("get hr: %v", err)
	}
	return &hr
}

func TestReconcile_PokesStuckHR(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	r, rec := newReconciler(t, now, stuckHR("a", "org-x", nil))

	res := reconcile(t, r, "a", "org-x")
	if res.RequeueAfter != r.Backoff {
		t.Errorf("want RequeueAfter=%v, got %v", r.Backoff, res.RequeueAfter)
	}

	hr := getHR(t, r, "a", "org-x")
	if hr.Annotations[AnnotationPokeCount] != "1" {
		t.Errorf("want poke-count=1, got %q", hr.Annotations[AnnotationPokeCount])
	}
	if hr.Annotations[AnnotationRequestedAt] == "" || hr.Annotations[AnnotationForceAt] == "" {
		t.Errorf("force/requestedAt annotations not set: %+v", hr.Annotations)
	}
	if hr.Annotations[AnnotationLastPokeAt] != hr.Annotations[AnnotationForceAt] {
		t.Errorf("last-poke-at and forceAt should match token")
	}

	select {
	case ev := <-rec.Events:
		if !contains(ev, "RecoveryPoke") {
			t.Errorf("want RecoveryPoke event, got %q", ev)
		}
	default:
		t.Errorf("no event recorded")
	}
}

func TestReconcile_SkipAnnotation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hr := stuckHR("a", "org-x", map[string]string{AnnotationSkip: "true"})
	r, _ := newReconciler(t, now, hr)

	reconcile(t, r, "a", "org-x")

	got := getHR(t, r, "a", "org-x")
	if _, ok := got.Annotations[AnnotationPokeCount]; ok {
		t.Errorf("skip annotation should prevent poke; got annotations: %+v", got.Annotations)
	}
}

func TestReconcile_Suspended(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hr := stuckHR("a", "org-x", nil)
	hr.Spec.Suspend = true
	r, _ := newReconciler(t, now, hr)

	reconcile(t, r, "a", "org-x")

	got := getHR(t, r, "a", "org-x")
	if _, ok := got.Annotations[AnnotationPokeCount]; ok {
		t.Errorf("suspended HR must not be poked")
	}
}

func TestReconcile_BackoffActive(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	lastNano := strconv.FormatInt(now.Add(-time.Minute).UnixNano(), 10) // 1 min ago
	hr := stuckHR("a", "org-x", map[string]string{
		AnnotationLastPokeAt: lastNano,
		AnnotationPokeCount:  "1",
	})
	r, _ := newReconciler(t, now, hr)

	res := reconcile(t, r, "a", "org-x")

	want := r.Backoff - time.Minute
	if res.RequeueAfter != want {
		t.Errorf("want RequeueAfter=%v, got %v", want, res.RequeueAfter)
	}
	got := getHR(t, r, "a", "org-x")
	if got.Annotations[AnnotationPokeCount] != "1" {
		t.Errorf("poke-count should still be 1, got %q", got.Annotations[AnnotationPokeCount])
	}
}

func TestReconcile_BackoffElapsed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	lastNano := strconv.FormatInt(now.Add(-10*time.Minute).UnixNano(), 10)
	hr := stuckHR("a", "org-x", map[string]string{
		AnnotationLastPokeAt: lastNano,
		AnnotationPokeCount:  "1",
	})
	r, _ := newReconciler(t, now, hr)

	reconcile(t, r, "a", "org-x")

	got := getHR(t, r, "a", "org-x")
	if got.Annotations[AnnotationPokeCount] != "2" {
		t.Errorf("want poke-count=2 after second poke, got %q", got.Annotations[AnnotationPokeCount])
	}
}

func TestReconcile_GiveUp(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hr := stuckHR("a", "org-x", map[string]string{
		AnnotationPokeCount: "10",
	})
	r, rec := newReconciler(t, now, hr)

	reconcile(t, r, "a", "org-x")

	got := getHR(t, r, "a", "org-x")
	if got.Annotations[AnnotationPokeCount] != "10" {
		t.Errorf("poke-count should remain 10 after giveup, got %q", got.Annotations[AnnotationPokeCount])
	}
	if got.Annotations[AnnotationRequestedAt] != "" {
		t.Errorf("must not poke when giving up; requestedAt=%q", got.Annotations[AnnotationRequestedAt])
	}

	select {
	case ev := <-rec.Events:
		if !contains(ev, "RecoveryGaveUp") {
			t.Errorf("want RecoveryGaveUp event, got %q", ev)
		}
	default:
		t.Errorf("no give-up event recorded")
	}
}

func TestReconcile_ResetsCountOnReady(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hr := readyHR("a", "org-x", map[string]string{AnnotationPokeCount: "3"})
	r, _ := newReconciler(t, now, hr)

	reconcile(t, r, "a", "org-x")

	got := getHR(t, r, "a", "org-x")
	if got.Annotations[AnnotationPokeCount] != "0" {
		t.Errorf("want poke-count reset to 0, got %q", got.Annotations[AnnotationPokeCount])
	}
}

func TestReconcile_ReadyZeroCountIsNoop(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hr := readyHR("a", "org-x", map[string]string{AnnotationPokeCount: "0"})
	r, _ := newReconciler(t, now, hr)

	// Should be a no-op. Patch the in-store object's annotations and confirm
	// the reconciler doesn't trample them.
	hr.Annotations["sentinel"] = "keep"
	if err := r.Update(context.Background(), hr); err != nil {
		t.Fatalf("update: %v", err)
	}

	reconcile(t, r, "a", "org-x")

	got := getHR(t, r, "a", "org-x")
	if got.Annotations["sentinel"] != "keep" {
		t.Errorf("unrelated annotations should not be touched, got %+v", got.Annotations)
	}
}

func TestReconcile_NotStuckAndNotReadyIsNoop(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hr := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "org-x"},
	}
	r, _ := newReconciler(t, now, hr)

	reconcile(t, r, "a", "org-x")

	got := getHR(t, r, "a", "org-x")
	if _, ok := got.Annotations[AnnotationPokeCount]; ok {
		t.Errorf("must not poke a HR that isn't stuck")
	}
}

func TestReconcile_BeingDeleted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hr := stuckHR("a", "org-x", nil)
	deletionTime := metav1.NewTime(now.Add(-time.Minute))
	hr.DeletionTimestamp = &deletionTime
	hr.Finalizers = []string{"finalizers.fluxcd.io"}
	r, _ := newReconciler(t, now, hr)

	reconcile(t, r, "a", "org-x")

	got := getHR(t, r, "a", "org-x")
	if _, ok := got.Annotations[AnnotationPokeCount]; ok {
		t.Errorf("HR being deleted must not be poked, got annotations: %+v", got.Annotations)
	}
}

func TestInScope_NamespacePrefix(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	hr := stuckHR("a", "kube-system", nil)
	r, _ := newReconciler(t, now, hr)
	r.NamespacePrefix = "org-"

	reconcile(t, r, "a", "kube-system")

	got := getHR(t, r, "a", "kube-system")
	if _, ok := got.Annotations[AnnotationPokeCount]; ok {
		t.Errorf("namespace-prefix scope must skip kube-system, got annotations: %+v", got.Annotations)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
