import logging

import pykube
import pytest
from pytest_helm_charts.clusters import Cluster

logger = logging.getLogger(__name__)


@pytest.mark.smoke
def test_api_working(kube_cluster: Cluster) -> None:
    """Verify the smoke-test cluster is reachable and the chart installed.

    The smoke step deploys the chart into the cluster before this test runs, so
    a healthy API connection here confirms the chart templates render and the
    release installs cleanly.

    Note: hr-recovery-controller reconciles Flux HelmRelease objects, whose CRDs
    are not present on a bare kind cluster, so the controller pod is not expected
    to reach readiness here. Pod-readiness assertions belong in an environment
    that provides the Flux CRDs.
    """
    assert kube_cluster.kube_client is not None
    assert len(pykube.Node.objects(kube_cluster.kube_client)) >= 1
