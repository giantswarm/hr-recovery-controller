# hr-recovery-controller

Controller that auto-recovers HelmReleases wedged on Stalled=MissingRollbackTarget by force-poking them.

**Homepage:** <https://github.com/giantswarm/hr-recovery-controller>

## Source Code

* <https://github.com/giantswarm/hr-recovery-controller>

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| image.name | string | `"giantswarm/hr-recovery-controller"` |  |
| image.tag | string | `""` |  |
| registry.domain | string | `"gsoci.azurecr.io"` |  |
| controller.maxPokes | int | `10` |  |
| controller.backoff | string | `"5m"` |  |
| controller.watchLabelSelector | string | `""` |  |
| resources.requests.cpu | string | `"25m"` |  |
| resources.requests.memory | string | `"64Mi"` |  |
| resources.limits.cpu | string | `"200m"` |  |
| resources.limits.memory | string | `"256Mi"` |  |
| podSecurityContext.runAsNonRoot | bool | `true` |  |
| podSecurityContext.seccompProfile.type | string | `"RuntimeDefault"` |  |
| securityContext.allowPrivilegeEscalation | bool | `false` |  |
| securityContext.readOnlyRootFilesystem | bool | `true` |  |
| securityContext.capabilities.drop[0] | string | `"ALL"` |  |
| metrics.port | int | `8080` |  |
