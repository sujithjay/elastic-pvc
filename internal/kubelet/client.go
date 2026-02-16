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

// NodeError records a failure to retrieve stats from a specific node.
type NodeError struct {
	NodeName string
	Err      error
}

// MetricsResult holds the outcome of a GetMetrics call.
type MetricsResult struct {
	Stats       map[types.NamespacedName]*VolumeStats
	FailedNodes []NodeError
}

// HasFailures returns true if any nodes failed to respond.
func (r *MetricsResult) HasFailures() bool {
	return len(r.FailedNodes) > 0
}

// MetricsClient retrieves PVC volume usage metrics from the cluster.
type MetricsClient interface {
	// GetMetrics returns volume stats keyed by PVC namespace/name.
	// Only PVCs currently mounted by a pod will have stats.
	// If nodeNames is non-empty, only those nodes are queried.
	// If nodeNames is empty or nil, all nodes are queried.
	// Returns partial results from successful nodes even if some nodes fail.
	GetMetrics(ctx context.Context, nodeNames []string) (*MetricsResult, error)
}
