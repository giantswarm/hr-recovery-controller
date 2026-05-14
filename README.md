[![CircleCI](https://dl.circleci.com/status-badge/img/gh/giantswarm/hr-recovery-controller/tree/main.svg?style=svg)](https://dl.circleci.com/status-badge/redirect/gh/giantswarm/hr-recovery-controller/tree/main)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/giantswarm/hr-recovery-controller/badge)](https://securityscorecards.dev/viewer/?uri=github.com/giantswarm/hr-recovery-controller)

[Guide about how to manage an app on Giant Swarm](https://handbook.giantswarm.io/docs/dev-and-releng/app-developer-processes/adding_app_to_appcatalog/)

# hr-recovery-controller

A small Kubernetes controller that auto-recovers Flux `HelmRelease` resources wedged on `Stalled=True, reason=MissingRollbackTarget`. This condition is hit during the Giant Swarm App-CR → HelmRelease migration when a transient Kyverno admission-controller restart causes a helm upgrade to fail, and helm-controller cannot roll back because the previous release was installed by chart-operator (no `Snapshot` in `status.History`).

**What it does.** Watches all `HelmRelease` objects, detects the wedge fingerprint, and patches `reconcile.fluxcd.io/forceAt` + `reconcile.fluxcd.io/requestedAt` with a fresh token to force helm-controller to retry the upgrade. By the time the controller pokes, the underlying Kyverno race has long settled, so the retry succeeds on first try.

**Why we ship it.** Manual `flux suspend` / `flux resume` is operationally untenable for a fleet-wide migration. The controller is finite-life: it will be retired once the Kyverno chart ships a `preStop` hook closing the EndpointSlice-vs-listener propagation gap.

**Detection criteria.** An HR is poked when:
- `status.conditions[Stalled].status == True`
- `status.conditions[Stalled].reason == MissingRollbackTarget`

**Skip conditions:**
- `metadata.deletionTimestamp` set (HR is being deleted)
- `spec.suspend == true`
- annotation `hr-recovery.giantswarm.io/skip: "true"`
- annotation `hr-recovery.giantswarm.io/poke-count` ≥ `--max-pokes` (default 10)
- last poke less than `--backoff` ago (default 5m)

**Metrics** (`/metrics` on `:8080`):
- `hr_recovery_pokes_total{namespace,hr_name}`
- `hr_recovery_successes_total{namespace,hr_name}` (incremented on `Ready=True` after at least one poke)
- `hr_recovery_giveups_total{namespace,hr_name}` (alert on this — a give-up means a human needs to look)

See `PLAN_UNSTUCK_CONTROLLER.md` in this repo for the full design rationale and validation plan.
