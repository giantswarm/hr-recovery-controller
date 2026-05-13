package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	pokesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hr_recovery_pokes_total",
			Help: "Number of times the controller has poked a stuck HelmRelease.",
		},
		[]string{"namespace", "hr_name"},
	)

	successesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hr_recovery_successes_total",
			Help: "Number of HelmReleases that recovered to Ready=True after at least one poke.",
		},
		[]string{"namespace", "hr_name"},
	)

	giveupsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hr_recovery_giveups_total",
			Help: "Number of times the controller has given up on a stuck HelmRelease after exceeding max-pokes.",
		},
		[]string{"namespace", "hr_name"},
	)
)

func init() {
	metrics.Registry.MustRegister(pokesTotal, successesTotal, giveupsTotal)
}
