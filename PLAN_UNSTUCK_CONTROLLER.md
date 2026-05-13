# Handoff: Auto-recovery controller for stuck HelmReleases during App-CR → HelmRelease migration

This document hands off the design and rationale for a small Kubernetes controller that auto-recovers HelmReleases that get wedged into a `Stalled=True, reason=MissingRollbackTarget` state during the App-CR → HelmRelease migration. It is self-contained: read it cold and you should be able to pick up implementation without other context.

## Background

We are migrating Giant Swarm default apps from the in-house `App` CR / `chart-operator` model to Flux `HelmRelease` CRs managed by `helm-controller`. The migration runs as a helm pre-upgrade hook on the `cluster-aws` chart: it pauses every `App` CR + `Chart` CR, strips their finalizers, deletes them, and lets the cluster-aws upgrade render the new `HelmRelease` set. Flux helm-controller then *adopts* the pre-existing helm releases on each workload cluster (the ones originally installed by chart-operator at cluster-create time, recorded in the WC's helm storage as `sh.helm.release.v1.<release>.v1`) by performing a helm upgrade against them, producing `.v2`.

The migration's primary safety guarantee — verified across 81 cluster-migrations and counting — is that **no helm release on the workload cluster gets uninstalled**. `.v1` baselines remain intact through the entire flow.

## The problem

In ~15-20% of cluster migrations (1-2 of 5 clusters per round), one HelmRelease ends up wedged. The fingerprint is:

```yaml
status:
  conditions:
    - type: Ready
      status: "False"
      reason: UpgradeFailed
      message: "Helm upgrade failed for release ...: cannot patch ... ClusterPolicy: ... failed calling webhook ... kyverno-svc.kyverno.svc:443: connect: connection refused"
    - type: Stalled
      status: "True"
      reason: MissingRollbackTarget
      message: "Failed to perform remediation: missing target release for rollback: cannot remediate failed release"
  upgradeFailures: 1
```

Two independent bugs combine to produce the wedge:

### Bug 1 — Kyverno admission-controller rolling-restart race

When the kyverno chart upgrades (chart-operator-installed v1 → helm-controller-managed v2), its admission-controller `Deployment` rolls. Even with `replicas: 3` and `maxUnavailable: 40%` (=1), there is a reliably-reproducible ~350 ms window per pod replacement where:

1. The terminating pod has received SIGTERM and closed its TLS listener on :9443.
2. The Kubernetes EndpointSlice for `kyverno-svc` has *not yet* been updated to remove that pod from the `ready`/`serving` set.
3. The cluster's Service data plane (Cilium in kube-proxy replacement mode in our test environment) still load-balances new connections to the dying pod.
4. The pod's kernel receives the TCP SYN, finds no listener, returns RST → caller sees `connect: connection refused`.

Any chart upgrade that PATCHes a Kyverno-validated resource (`ClusterPolicy`, `PolicyException`, `Policy`) during this window invokes the kyverno webhook and gets the connection refused. We've reproduced this in a controlled experiment (`/tmp/kyverno-repro/run.sh` if still on disk) with timestamped EndpointSlice watches that confirm the propagation gap.

Affected charts in our migration: the kyverno chart's own bundled ClusterPolicies (`restrict-polex-namespaces`, `restrict-policy-kind-wildcards`, etc.), `kyverno-policies`, `k8s-audit-metrics`, `cert-exporter`, `cluster-autoscaler`, `etcd-defrag`, `etcd-k8s-res-count-exporter`, `k8s-dns-node-cache`, `node-exporter`, `observability-policies`, `alloy-events`, `alloy-logs`, `alloy-metrics`, `aws-ebs-csi-driver` — anything shipping a Kyverno-validated resource.

This race was **introduced (or made noticeably more frequent) by the kyverno-app v0.24.2 bump landed in security-bundle on 2026-05-04** (commit `824c5d0`). v0.24.2 brought upstream kyverno v1.17.2 (changes image tag → guarantees rolling restart on every chart upgrade), enabled HPA on admission-controller (more pod transitions), and added CAPI taint tolerations. Before that bump, 7 consecutive migration rounds (48-54) all passed 5/5 fully clean. After it, every round except one (Round 61) has hit the race on at least one cluster.

### Bug 2 — Flux helm-controller v1.3.0 cannot roll back to a chart-operator-installed release

When the kyverno race causes the upgrade to fail, helm-controller's default remediation strategy is `rollback`. The rollback path verifies the previous release via `internal/action/verify.go:VerifySnapshot`, which requires a `Snapshot` entry in the HR's `status.History`. Because `.v1` was installed by chart-operator-WC (not helm-controller), there's no Snapshot entry for it. `VerifySnapshot` returns `ErrReleaseNotFound` → `internal/reconcile/atomic_release.go:200` unconditionally sets:

```go
conditions.MarkStalled(req.Object, "MissingRollbackTarget",
    "Failed to perform remediation: %s", err)
```

Once `Stalled=True` is on the HR, helm-controller refuses to reconcile further unless one of three things happens:
1. The HR's spec changes (generation bump).
2. The HR gets a `reconcile.fluxcd.io/requestedAt` annotation with a new token (matching `status.lastHandledReconcileAt`).
3. The HR gets a `reconcile.fluxcd.io/forceAt` annotation with a new token (matching `status.lastHandledForceAt`).

The `force` path is special: in `atomic_release.go:453` it skips the remediation step entirely and re-attempts the upgrade directly. This is exactly what we need, because by the time we poke a stuck HR, the kyverno admission-controller has long settled and the retry succeeds on first try.

### Current manual recovery

Operators currently unblock each wedged HR with:

```sh
flux --context <mc> -n <ns> suspend hr <name>
sleep 10
flux --context <mc> -n <ns> resume hr <name>
```

This works because suspend/resume mutates the spec, triggering generation change, triggering reconciliation. On resume helm-controller re-evaluates and re-attempts the upgrade. Within seconds the HR converges.

`flux reconcile hr <name> --force` works identically and is one command shorter. Both effectively patch the same annotations under the hood.

We've measured the per-cluster wedge rate at ~15-20% across rounds 56-66. Each manual recovery costs ~30 seconds of SRE attention plus typing. **For a real production fleet-wide migration of dozens to hundreds of clusters, this is operationally untenable.**

## Why we're not fixing the root cause directly (yet)

The clean fix is a `preStop` lifecycle hook with `sleep 10` and `terminationGracePeriodSeconds: 30` on the kyverno admission-controller container. This closes the listener-vs-EndpointSlice propagation gap entirely. Documented separately. Two paths to ship:

1. PR to upstream `kyverno/kyverno` chart adding a `lifecycle` value key (slow — community review).
2. Patch the vendored upstream chart in `giantswarm/kyverno-app` (faster — we control the repo). See `kyverno-race.html` and earlier conversation history for the implementation sketch.

Whichever path lands, it takes time. Meanwhile, the migration is being executed across our fleet and SREs are eating the manual recovery cost. **This handoff is about building a stop-gap controller that bridges that window.**

The controller is explicitly finite-life: it should be deprecated and removed once kyverno's preStop hook is shipped to every workload cluster.

## Proposed solution: `hr-recovery-controller`

A small controller that watches `HelmRelease` resources, detects the wedge fingerprint, and pokes the HR with a `force` annotation to bypass remediation and trigger one upgrade attempt.

### Detection criteria

An HR is a candidate for recovery if **all** of:

1. `status.conditions[?(@.type=="Stalled")].status == "True"`.
2. `status.conditions[?(@.type=="Stalled")].reason == "MissingRollbackTarget"`.

Starting with this narrow signature is intentional. It targets exactly the wedge we know about. Other Flux stall reasons (e.g. ACL access denied, dependency-not-ready persistent) shouldn't be auto-recovered without separate analysis.

Optionally, you can broaden later if other patterns emerge:
- `Stalled=True` with `reason=ExceededMaxRetries` after `upgradeFailures` plateaus.
- `Ready=False, reason=UpgradeFailed` AND `lastAttemptedRevision` unchanged for >5 minutes (indicating helm-controller has given up).

Skip the HR if **any** of:

1. `spec.suspend == true` (operator is intentionally holding it).
2. Annotation `hr-recovery.giantswarm.io/skip == "true"` (per-HR opt-out for SRE investigations).
3. Annotation `hr-recovery.giantswarm.io/poke-count >= <max>` (give-up threshold; recommend default 10).
4. Annotation `hr-recovery.giantswarm.io/last-poke-at` timestamp less than backoff period ago (recommend default 5 minutes — gives helm-controller time to actually run an upgrade attempt before we poke again).

### Recovery action

Patch the HR with three annotations, all set to the same fresh token:

```yaml
metadata:
  annotations:
    reconcile.fluxcd.io/requestedAt: "<token>"
    reconcile.fluxcd.io/forceAt: "<token>"
    hr-recovery.giantswarm.io/last-poke-at: "<token>"
    hr-recovery.giantswarm.io/poke-count: "<N+1>"
```

The token must change each invocation (otherwise helm-controller no-ops via `lastHandledForceAt` comparison). Unix nano timestamp from `date +%s%N` works.

The first two annotations are the helm-controller protocol. The third pair is our own bookkeeping (persisted on the HR, no separate state store needed).

After helm-controller successfully re-attempts the upgrade and the HR transitions to `Ready=True`, the controller should reset the `poke-count` annotation back to 0 on the next reconcile (so future independent stalls get a fresh counter).

### Implementation form: small Go controller (controller-runtime)

Ship this as a single-binary Go controller using `sigs.k8s.io/controller-runtime`. The two reasons this is the right form, not a polling CronJob:

1. **API load.** A watch is dramatically cheaper than periodic list. A CronJob doing `kubectl get hr -A` every minute lists every HR on the MC every cycle — on a busy MC, that's many hundreds of objects per minute, indefinitely, even when nothing is stuck. A controller opens one watch (served from the apiserver's watch cache) and receives only deltas. Steady-state cost while nothing is wedged is approximately zero.
2. **Reaction time.** A controller reacts within sub-second of `Stalled=True` being set, because the watch fires on the status transition. A CronJob has up to 60s of latency by definition. In a fleet-wide migration with multiple wedges per hour, that matters both operationally and perceptually.

There are secondary benefits — the predicate filter, `EventRecorder`, built-in `/metrics` endpoint, easier testability with `envtest` — but the apiserver-load and latency arguments alone are decisive.

The controller needs:

1. A `ServiceAccount` in some control-plane namespace (e.g., `flux-giantswarm` or `giantswarm`).
2. A `ClusterRole` granting `get,list,watch,patch` on `helmreleases.helm.toolkit.fluxcd.io`, and `create,patch` on `events`. No write on other resources.
3. A `ClusterRoleBinding` to the SA.
4. A `Deployment` with `replicas: 1` running the controller binary.
5. A `Service` exposing `/metrics` for Prometheus scrape.

#### Controller logic

Watch `HelmRelease` resources cluster-wide. Use a predicate to enqueue only HRs whose status matches the wedge fingerprint AND whose status transitioned (avoid spurious enqueues on resync).

```go
// pseudocode
func stuckPredicate() predicate.Predicate {
    isStuck := func(obj client.Object) bool {
        hr, ok := obj.(*helmv2.HelmRelease)
        if !ok { return false }
        for _, c := range hr.Status.Conditions {
            if c.Type == "Stalled" && c.Status == metav1.ConditionTrue &&
               c.Reason == "MissingRollbackTarget" {
                return true
            }
        }
        return false
    }
    // We also want to react when a previously-stuck HR becomes Ready,
    // so we can reset the poke-count. So predicate allows both.
    wasStuckOrNowReady := func(old, new client.Object) bool {
        return isStuck(new) || (isStuck(old) && isReady(new))
    }
    return predicate.Funcs{
        CreateFunc: func(e event.CreateEvent) bool { return isStuck(e.Object) },
        UpdateFunc: func(e event.UpdateEvent) bool { return wasStuckOrNowReady(e.ObjectOld, e.ObjectNew) },
        DeleteFunc: func(event.DeleteEvent) bool { return false },
    }
}
```

The reconcile loop:

```go
// pseudocode
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    var hr helmv2.HelmRelease
    if err := r.Get(ctx, req.NamespacedName, &hr); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // Reset poke-count on success.
    if isReady(&hr) && hr.Annotations["hr-recovery.giantswarm.io/poke-count"] != "" &&
       hr.Annotations["hr-recovery.giantswarm.io/poke-count"] != "0" {
        return ctrl.Result{}, r.patchAnnotations(ctx, &hr, map[string]string{
            "hr-recovery.giantswarm.io/poke-count": "0",
        })
    }

    if !isStalledWithMissingRollbackTarget(&hr) {
        return ctrl.Result{}, nil
    }

    // Skip conditions.
    if hr.Spec.Suspend { return ctrl.Result{}, nil }
    if hr.Annotations["hr-recovery.giantswarm.io/skip"] == "true" {
        return ctrl.Result{}, nil
    }

    // Backoff.
    last, _ := parseUnixNano(hr.Annotations["hr-recovery.giantswarm.io/last-poke-at"])
    if elapsed := time.Since(last); elapsed < r.Backoff {
        return ctrl.Result{RequeueAfter: r.Backoff - elapsed}, nil
    }

    // Give-up.
    count, _ := strconv.Atoi(hr.Annotations["hr-recovery.giantswarm.io/poke-count"])
    if count >= r.MaxPokes {
        r.Recorder.Event(&hr, "Warning", "RecoveryGaveUp",
            fmt.Sprintf("Gave up after %d pokes", count))
        autoRecoveryGiveupsTotal.WithLabelValues(req.Namespace, req.Name).Inc()
        return ctrl.Result{}, nil
    }

    // Poke.
    token := strconv.FormatInt(time.Now().UnixNano(), 10)
    if err := r.patchAnnotations(ctx, &hr, map[string]string{
        "reconcile.fluxcd.io/requestedAt":             token,
        "reconcile.fluxcd.io/forceAt":                  token,
        "hr-recovery.giantswarm.io/last-poke-at":    token,
        "hr-recovery.giantswarm.io/poke-count":      strconv.Itoa(count + 1),
    }); err != nil {
        return ctrl.Result{}, err
    }

    r.Recorder.Event(&hr, "Normal", "RecoveryPoke",
        fmt.Sprintf("Poke %d (token %s)", count+1, token))
    autoRecoveryPokesTotal.WithLabelValues(req.Namespace, req.Name).Inc()

    return ctrl.Result{RequeueAfter: r.Backoff}, nil
}
```

Use `client.MergeFrom`-style patches (not full Update) to avoid generation bumps and resource-version conflicts. The annotation patch should be a strategic merge or JSON merge patch against `metadata.annotations` only.

#### Metrics

Expose at least:

- `hr_recovery_pokes_total{namespace,hr_name}` counter.
- `hr_recovery_successes_total{namespace,hr_name}` counter (incremented when poke-count is reset to 0 after Ready=True).
- `hr_recovery_giveups_total{namespace,hr_name}` counter.
- `hr_recovery_stuck_hrs_current` gauge (count of HRs currently matching the wedge fingerprint, derived from the informer cache via a custom collector or periodic scan).

Alert on `hr_recovery_giveups_total > 0` — give-ups should never happen in steady state; if one fires, the chart is genuinely broken in a way the controller can't fix and a human needs to look.

#### Configuration

Keep it small — the controller has a single job. Reasonable knobs:

- `--max-pokes` (default 10): give-up threshold per HR.
- `--backoff` (default 5m): minimum interval between pokes for the same HR.
- `--namespace-prefix` (optional, e.g. `org-`): if set, only act on HRs in matching namespaces. Hard-stop safety against blanket production reach.
- `--watch-label-selector` (optional): further scope by label, e.g. `giantswarm.io/managed-by=cluster-aws`.

#### Project layout

A minimum-viable layout:

```
hr-recovery-controller/
  main.go                       (~50 lines: manager setup, flags)
  internal/controller/
    reconciler.go               (~120 lines: Reconcile + helpers)
    predicate.go                (~30 lines: stuckPredicate)
    metrics.go                  (~30 lines: prometheus collectors)
  internal/controller/
    reconciler_test.go          (~150 lines: envtest-based)
  Dockerfile                    (~10 lines, multi-stage Go build)
  helm/hr-recovery-controller/
    Chart.yaml
    templates/
      serviceaccount.yaml
      clusterrole.yaml
      clusterrolebinding.yaml
      deployment.yaml
      service.yaml
    values.yaml
```

Total source: ~400 lines of Go plus ~80 lines of helm chart YAML and a small Dockerfile. Use `kubebuilder init` or just copy from an existing small Giant Swarm controller as a starting template.

#### Fallback: CronJob form (only if Go controller is infeasible)

If for organizational reasons a Go controller can't be shipped (e.g., no team currently owns Go-based MC controllers, kubebuilder bootstrap is a hard blocker), a kubectl-based CronJob is documented at the end of this file as a fallback. It works functionally but is strictly worse on API load, latency, and observability. Do not pick this form unless you have a concrete reason the Go controller can't ship.

### Observability

Whatever form you pick, surface these signals:

- Kubernetes Events on each poked HR (`type: Normal, reason: RecoveryPoke`). `kubectl describe hr` should show them.
- Prometheus metrics (if controller form): `hr_recovery_pokes_total{namespace,name}`, `hr_recovery_giveups_total`, `hr_recovery_stuck_hrs` (gauge).
- Alertmanager rule: `hr_recovery_giveups_total > 0` should page — it means a chart is genuinely broken in a way the controller can't fix.
- The poke-count annotation on each HR doubles as a per-HR health indicator.

### Safety constraints

- **Scope it.** Don't poke arbitrary HRs in production org namespaces unless you mean to. Consider filtering by:
  - Namespace prefix (`org-*` only).
  - Label selector (e.g., `giantswarm.io/managed-by=cluster-aws`).
  - Or an explicit opt-in annotation on each HR's parent chart.
- **Cap retries.** Default 10 pokes per HR before giving up. Anything genuinely broken (chart bug, immutable-field change, schema validation failure) will not be fixed by retrying; the poker would just retry-storm forever. The cap forces a human into the loop after sensible automation has failed.
- **Per-HR backoff.** Don't poke the same HR more than once per ~5 minutes. helm-controller needs time to actually run the upgrade attempt; poking again before that risks confusing the state machine.
- **Respect `spec.suspend`.** If an SRE has suspended the HR, leave it alone.
- **Respect a skip annotation.** Per-HR escape hatch (`hr-recovery.giantswarm.io/skip: "true"`).

## Fallback only: CronJob skeleton

The following is a working starting point for the CronJob fallback. **Prefer the Go controller above.** Use this only if Go is infeasible for organizational reasons. Adjust the namespace, image, RBAC scope, and skip annotation domain to match Giant Swarm conventions.

```yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: hr-recovery-controller
  namespace: flux-giantswarm
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hr-recovery-controller
rules:
  - apiGroups: ["helm.toolkit.fluxcd.io"]
    resources: ["helmreleases"]
    verbs: ["get", "list", "patch"]
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: hr-recovery-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: hr-recovery-controller
subjects:
  - kind: ServiceAccount
    name: hr-recovery-controller
    namespace: flux-giantswarm
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: hr-recovery-controller
  namespace: flux-giantswarm
spec:
  schedule: "* * * * *"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 1
  failedJobsHistoryLimit: 3
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: hr-recovery-controller
          restartPolicy: Never
          containers:
            - name: recover
              image: gsoci.azurecr.io/giantswarm/kubectl:1.34.5
              command: ["/bin/sh", "-c"]
              args:
                - |
                  set -eu
                  MAX_POKES=10
                  BACKOFF_SEC=300
                  now=$(date +%s)

                  kubectl get hr -A -o json | jq -r --argjson now "$now" --argjson backoff "$BACKOFF_SEC" '
                    .items[]
                    | select(.spec.suspend != true)
                    | select(.metadata.annotations["hr-recovery.giantswarm.io/skip"] != "true")
                    | select(
                        .status.conditions[]?
                        | select(.type == "Stalled" and .status == "True" and .reason == "MissingRollbackTarget")
                      )
                    | . as $hr
                    | ($hr.metadata.annotations["hr-recovery.giantswarm.io/last-poke-at"] // "0" | tonumber / 1000000000 | floor) as $last
                    | select(($now - $last) >= $backoff)
                    | "\($hr.metadata.namespace) \($hr.metadata.name) \($hr.metadata.annotations["hr-recovery.giantswarm.io/poke-count"] // "0")"
                  ' | while read -r ns name prev_count; do
                    count=$((prev_count + 1))
                    if [ "$count" -gt "$MAX_POKES" ]; then
                      echo "GIVING_UP ns=$ns hr=$name pokes=$prev_count"
                      continue
                    fi
                    token=$(date +%s%N)
                    echo "POKE ns=$ns hr=$name count=$count token=$token"
                    kubectl -n "$ns" annotate hr "$name" \
                      "reconcile.fluxcd.io/requestedAt=$token" \
                      "reconcile.fluxcd.io/forceAt=$token" \
                      "hr-recovery.giantswarm.io/last-poke-at=$token" \
                      "hr-recovery.giantswarm.io/poke-count=$count" \
                      --overwrite >/dev/null
                  done
```

Required tooling in the image: `kubectl`, `jq`, `sh`. The giantswarm/kubectl image typically has `jq`; verify or switch to a more comprehensive base.

## Validation plan

A fresh agent picking up this work should validate as follows:

1. **Deploy the controller** (Go binary or fallback CronJob) on a non-production management cluster (e.g., `graveler`).
2. **Verify zero impact when nothing is stuck.** Watch the controller's `/metrics` endpoint and `kubectl logs`; with no wedged HRs in the cluster, `hr_recovery_pokes_total` should stay at zero and the reconcile loop should be silent. For the CronJob fallback, confirm runs log "no candidates" or similar.
3. **Create a stuck HR deliberately.** The kyverno race is easy to repro on a workload cluster:
   - Create a fresh workload cluster at release 34.1.0.
   - Trigger the migration to 35.0.0-jose (per `MIGRATE_APPS_TO_HELMRELEASES.md`).
   - Watch HRs converge; one is likely to wedge with the target signature.
   - Alternatively, run `/tmp/kyverno-repro/run.sh` (if still on disk) which reliably hits the race within ~30 seconds.
4. **Watch the recovery.** With the Go controller, the wedged HR should be poked within seconds of `Stalled=True` being set (watch-driven, sub-second). With the CronJob fallback, expect up to ~1 minute latency. After the poke, helm-controller should re-attempt the upgrade; within another ~30 seconds the HR should be `Ready=True`. Verify on the HR: `hr-recovery.giantswarm.io/poke-count=1` and `last-poke-at` set; verify the controller emitted an `RecoveryPoke` Event (visible via `kubectl describe hr <name>`); verify the metric `hr_recovery_pokes_total` incremented by 1; verify `hr_recovery_successes_total` incremented after the HR became Ready and `poke-count` was reset to 0.
5. **Verify the give-up safety.** Manually patch a stuck HR's annotations to `hr-recovery.giantswarm.io/poke-count: "9"` and set `last-poke-at` to a value older than the backoff. The controller should poke once more (count=10), then on subsequent transitions emit an `RecoveryGaveUp` Event without further pokes and increment `hr_recovery_giveups_total`. Confirm no infinite-loop behavior.
6. **Verify suspend is respected.** `flux suspend hr <name>` on a stuck HR; confirm the controller does not poke it. The predicate or reconcile should explicitly skip.
7. **Verify skip annotation works.** Set `hr-recovery.giantswarm.io/skip: "true"` on a stuck HR; confirm it's skipped.
8. **Verify per-HR backoff.** Force two pokes within the backoff window (e.g., manually delete the `last-poke-at` annotation between attempts and observe that the controller re-pokes immediately, vs. leaving it set and observing that the controller defers). Confirms the backoff logic is honored.
9. **Run a full migration round (5 clusters)** with the controller deployed. The success criterion: previously-1/5-stuck rounds now finish 5/5 clean, with poke counts visible on at most a few HRs, no give-ups, no `RecoveryGaveUp` events fired.

## Lifecycle and retirement

This controller exists to bridge the window between:
- **Today:** kyverno admission-controller chart lacks a preStop hook → rolling restart leaves a propagation gap → ~15-20% of cluster migrations wedge on the rollback-target bug.
- **Future:** kyverno chart ships preStop hook → no rolling-restart race → no wedges → controller becomes silently inactive.

Once the kyverno chart fix is shipped to every workload cluster AND a few migration rounds show zero wedges from the kyverno race, the controller can be removed without functional change. Track this explicitly:

1. Set a metric/alert that says "no pokes in N days = controller is dormant, candidate for removal".
2. Add a comment to the CronJob manifest pointing back to this document and the kyverno preStop fix tracking issue.
3. Document the retirement step in the migration project's runbook.

If, after the kyverno fix lands, the controller is still firing on some other stall pattern, that's signal to investigate the new pattern — don't broaden the controller's detection criteria without understanding what's stuck and why.

## What is intentionally out of scope

- **The kyverno chart fix itself.** Tracked separately (preStop + terminationGracePeriodSeconds in `giantswarm/kyverno-app` vendored chart). This handoff does not depend on that work and vice versa.
- **A broader-purpose "stuck HR" detector.** Other Flux stall patterns exist (`ExceededMaxRetries`, ACL denial, dependency-not-ready persistence, etc.). Each has different root causes and recovery semantics. Don't bundle them into this controller without separate analysis per pattern.
- **Migration-procedure changes.** The `cluster` migration hook does not need to change to support this controller. They are decoupled. The controller reacts to whatever helm-controller produces; it doesn't know or care that there's a hook.

## Key references

- Flux helm-controller v1.3.0 source code, especially `internal/reconcile/atomic_release.go` (the `MissingRollbackTarget` decision tree) and `api/v2/annotations.go` (the `forceAt`/`requestedAt`/`resetAt` annotation protocol).
- `MIGRATION_REPORT.md` in this repo: full round-by-round history of the migration tests including all kyverno-race occurrences and the controlled-repro evidence (Round 65 entry).
- `kyverno-race.html` in this repo: detailed writeup of the race mechanism and proposed kyverno-chart fix, shareable with the kyverno team.
- `MIGRATE_APPS_TO_HELMRELEASES.md` in this repo: the procedure to run a migration test round.
- `giantswarm/kyverno-app` repo (`vendir.yml`): where the kyverno chart preStop fix would land.

## Acceptance for the next agent

The next agent picking this up should aim to deliver, in order:

1. **Decide repository location.** Giant Swarm ships management-cluster controllers from dedicated repos. Confirm with the user where this one should live — probably a new repo following the conventions of existing small controllers, or as part of an existing flux-related repo. The controller will be deployed to MCs via the app platform, so it needs a published helm chart.
2. **Scaffold the Go controller** using `kubebuilder init` or by copying from an existing small Giant Swarm controller. Project layout described above.
3. **Implement the reconciler, predicate, and metrics** per the pseudocode and configuration above.
4. **Write unit/integration tests** using `envtest`. At minimum cover: wedge detection, skip conditions, backoff, give-up, poke-count reset on success.
5. **Add the helm chart** (manifests sketched above) and publish to a Giant Swarm OCI catalog.
6. **Deploy to `graveler` MC** for validation per the validation plan above.
7. **Run a full 5-cluster migration round** with the controller deployed and confirm the previously-1/5-stuck pattern becomes 5/5 clean (or at least clean after controller intervention, with poke counts visible but no SRE intervention).
8. **Append a short follow-up note to this document** with: where the controller landed, validation outcomes, any tweaks made vs the design above, and the metric/alert configured for the retirement signal.
9. **Set up the retirement-signal alert** (e.g., `hr_recovery_pokes_total` rate over 7 days = 0 → controller is dormant, candidate for removal). Reference the kyverno preStop fix tracking issue in the alert annotations so a future operator sees the connection.

If concrete blockers force the CronJob fallback (and only then), pick that form, deliver the equivalent manifests, and explicitly note the trade-off taken in the follow-up note.
