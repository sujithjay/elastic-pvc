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
)

func init() {
	metrics.Registry.MustRegister(rateLimitedTotal)
	metrics.Registry.MustRegister(resizesTotal)
}
