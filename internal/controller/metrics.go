package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// rateLimitedTotal counts PVC resizes skipped due to rate limiting.
	rateLimitedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "elastic_pvc_rate_limited_total",
			Help: "Total PVC resizes skipped due to rate limiting",
		},
		[]string{"reason"}, // "cooldown" or "per_cycle_limit"
	)

	// resizesTotal counts successful resize operations.
	resizesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "elastic_pvc_resizes_total",
			Help: "Total successful PVC resize operations",
		},
	)

	// nodeQueryFailuresTotal counts kubelet stats query failures.
	nodeQueryFailuresTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "elastic_pvc_node_query_failures_total",
			Help: "Total kubelet stats query failures",
		},
	)

	// reconcileDurationSeconds observes how long each reconciliation cycle takes.
	reconcileDurationSeconds = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "elastic_pvc_reconcile_duration_seconds",
			Help:    "Duration of each reconciliation cycle in seconds",
			Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120},
		},
	)
)

func init() {
	metrics.Registry.MustRegister(rateLimitedTotal)
	metrics.Registry.MustRegister(resizesTotal)
	metrics.Registry.MustRegister(nodeQueryFailuresTotal)
	metrics.Registry.MustRegister(reconcileDurationSeconds)
}
