import logging
from typing import List

import pykube
import pytest
import yaml
from pytest_helm_charts.clusters import Cluster
from pytest_helm_charts.k8s.deployment import wait_for_deployments_to_run

logger = logging.getLogger(__name__)

deployment_name = "hr-recovery-controller"
namespace_name = "hr-recovery-controller"

timeout: int = 180

# Minimal stub of the Flux HelmRelease CRD. The controller watches
# HelmReleases and crashloops until the CRD exists; the kind smoke cluster
# has no Flux installed, so we register the API ourselves. The schema is
# irrelevant for the smoke test -- the informer only needs the API to exist.
HELMRELEASE_CRD = """
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: helmreleases.helm.toolkit.fluxcd.io
spec:
  group: helm.toolkit.fluxcd.io
  names:
    kind: HelmRelease
    listKind: HelmReleaseList
    plural: helmreleases
    singular: helmrelease
    shortNames:
      - hr
  scope: Namespaced
  versions:
    - name: v2
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true
      subresources:
        status: {}
"""


@pytest.fixture(scope="module", autouse=True)
def helmrelease_crd(kube_cluster: Cluster) -> None:
    crd_obj = yaml.safe_load(HELMRELEASE_CRD)
    crd = pykube.CustomResourceDefinition(kube_cluster.kube_client, crd_obj)
    if not crd.exists():
        logger.info("Creating stub HelmRelease CRD..")
        crd.create()


@pytest.mark.smoke
def test_api_working(kube_cluster: Cluster) -> None:
    """Verify the smoke-test cluster is reachable through its Kubernetes API."""
    assert kube_cluster.kube_client is not None
    assert len(pykube.Node.objects(kube_cluster.kube_client)) >= 1


@pytest.fixture(scope="module")
def deployment(kube_cluster: Cluster) -> List[pykube.Deployment]:
    logger.info("Waiting for hr-recovery-controller deployment..")
    deployment_ready = wait_for_deployments_to_run(
        kube_cluster.kube_client,
        [deployment_name],
        namespace_name,
        timeout,
    )
    logger.info("hr-recovery-controller deployment looks satisfied..")
    return deployment_ready


@pytest.mark.smoke
@pytest.mark.upgrade
@pytest.mark.flaky(reruns=1, reruns_delay=15)
def test_pods_available(kube_cluster: Cluster, deployment: List[pykube.Deployment]):
    for s in deployment:
        assert int(s.obj["status"]["readyReplicas"]) == int(s.obj["spec"]["replicas"])
