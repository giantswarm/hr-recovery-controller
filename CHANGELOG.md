# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial controller implementation. Watches `HelmRelease` resources cluster-wide and force-pokes any HR wedged on `Stalled=True, reason=MissingRollbackTarget` (the chart-operator → helm-controller migration wedge).

[Unreleased]: https://github.com/giantswarm/hr-recovery-controller/tree/main
