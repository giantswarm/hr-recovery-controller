# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

- added: Initial controller implementation. Watches `HelmRelease` resources cluster-wide and force-pokes any HR wedged on `Stalled=True, reason=MissingRollbackTarget` (the chart-operator → helm-controller migration wedge). Configurable `--max-pokes`, `--backoff`, and `--watch-label-selector`. Exposes `hr_recovery_pokes_total`, `hr_recovery_successes_total`, and `hr_recovery_giveups_total` Prometheus counters.
- added: Helm chart manifests (ServiceAccount, ClusterRole/Binding, Deployment, Service) for deployment via the Giant Swarm app platform.
- changed: `app.giantswarm.io` label group was changed to `application.giantswarm.io`

[Unreleased]: https://github.com/giantswarm/hr-recovery-controller/tree/main
