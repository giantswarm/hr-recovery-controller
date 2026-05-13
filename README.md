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
- `spec.suspend == true`
- annotation `hr-recovery.giantswarm.io/skip: "true"`
- annotation `hr-recovery.giantswarm.io/poke-count` ≥ `--max-pokes` (default 10)
- last poke less than `--backoff` ago (default 5m)

**Metrics** (`/metrics` on `:8080`):
- `hr_recovery_pokes_total{namespace,hr_name}`
- `hr_recovery_successes_total{namespace,hr_name}` (incremented on `Ready=True` after at least one poke)
- `hr_recovery_giveups_total{namespace,hr_name}` (alert on this — a give-up means a human needs to look)

See `PLAN_UNSTUCK_CONTROLLER.md` in this repo for the full design rationale and validation plan.

## Installing

There are several ways to install this app onto a workload cluster.

- [Using GitOps to instantiate the App](https://docs.giantswarm.io/tutorials/continuous-deployment/apps/add-appcr/)
- By creating an [App resource](https://docs.giantswarm.io/reference/platform-api/crd/apps.application.giantswarm.io) using the platform API as explained in [Getting started with App Platform](https://docs.giantswarm.io/tutorials/fleet-management/app-platform/).

## Configuring

### values.yaml

**This is an example of a values file you could upload using our web interface.**

```yaml
# values.yaml

```

### Sample App CR and ConfigMap for the management cluster

If you have access to the Kubernetes API on the management cluster, you could create the App CR and ConfigMap directly.

Here is an example that would install the app to workload cluster `abc12`:

```yaml
# appCR.yaml

```

```yaml
# user-values-configmap.yaml

```

See our [full reference on how to configure apps](https://docs.giantswarm.io/tutorials/fleet-management/app-platform/app-configuration/) for more details.

## Compatibility

This app has been tested to work with the following workload cluster release versions:

- _add release version_

## Limitations

Some apps have restrictions on how they can be deployed.
Not following these limitations will most likely result in a broken deployment.

- _add limitation_

## Credit

- {APP HELM REPOSITORY}
