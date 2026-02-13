package kubelet

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
)

// VolumeStats holds filesystem usage data for a single PVC volume.
type VolumeStats struct {
	AvailableBytes int64
	CapacityBytes  int64
}

// MetricsClient retrieves PVC volume usage metrics from the cluster.
type MetricsClient interface {
	// GetMetrics returns volume stats keyed by PVC namespace/name.
	// Only PVCs currently mounted by a pod will have stats.
	GetMetrics(ctx context.Context) (map[types.NamespacedName]*VolumeStats, error)
}
