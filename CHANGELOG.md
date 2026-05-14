# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.0.4] - 2026-05-14

### Added

- NetworkPolicy that allows the controller pod to reach the kube-apiserver (egress all) and lets Prometheus scrape `/metrics` (ingress on the metrics port). Required because the `giantswarm` namespace has a `default-deny-all` policy that was silently blocking the informer.

## [0.0.3] - 2026-05-14

### Changed

- Add a circleci job to push the container image to the container registry.

## [0.0.2] - 2026-05-14

### Changed

- Bumped `sigs.k8s.io/controller-runtime` to v0.24.1 and the `k8s.io/{api,apimachinery,client-go}` modules to v0.36.1.
- Migrated from the deprecated `record.EventRecorder` (old events API) to `events.EventRecorder` (new events API).

## [0.0.1] - 2026-05-13

### Added

- Initial controller implementation. Watches `HelmRelease` resources cluster-wide and force-pokes any HR wedged on `Stalled=True, reason=MissingRollbackTarget` (the chart-operator → helm-controller migration wedge).

[Unreleased]: https://github.com/giantswarm/hr-recovery-controller/compare/v0.0.4...HEAD
[0.0.4]: https://github.com/giantswarm/hr-recovery-controller/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/giantswarm/hr-recovery-controller/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/giantswarm/hr-recovery-controller/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/giantswarm/hr-recovery-controller/releases/tag/v0.0.1
