package kubelet

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Kubelet Stats Summary API types.
// Only the fields we need are declared.

type statsSummary struct {
	Pods []podStats `json:"pods"`
}

type podStats struct {
	PodRef podReference  `json:"podRef"`
	Volume []volumeStats `json:"volume,omitempty"`
}

type podReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type volumeStats struct {
	Name           string        `json:"name"`
	PVCRef         *pvcReference `json:"pvcRef,omitempty"`
	UsedBytes      *int64        `json:"usedBytes,omitempty"`
	CapacityBytes  *int64        `json:"capacityBytes,omitempty"`
	AvailableBytes *int64        `json:"availableBytes,omitempty"`
}

type pvcReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// statsClient queries the kubelet /stats/summary endpoint on each node
// to collect PVC volume usage data.
type statsClient struct {
	clientset kubernetes.Interface
}

// NewStatsClient creates a MetricsClient that reads volume stats from kubelet.
func NewStatsClient(clientset kubernetes.Interface) MetricsClient {
	return &statsClient{clientset: clientset}
}

func (c *statsClient) GetMetrics(ctx context.Context) (map[types.NamespacedName]*VolumeStats, error) {
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	result := make(map[types.NamespacedName]*VolumeStats)
	var mu sync.Mutex

	eg, ctx := errgroup.WithContext(ctx)
	for _, node := range nodes.Items {
		nodeName := node.Name
		eg.Go(func() error {
			nodeStats, err := c.getNodeStats(ctx, nodeName)
			if err != nil {
				return fmt.Errorf("node %s: %w", nodeName, err)
			}
			mu.Lock()
			defer mu.Unlock()
			for k, v := range nodeStats {
				result[k] = v
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *statsClient) getNodeStats(ctx context.Context, nodeName string) (map[types.NamespacedName]*VolumeStats, error) {
	raw, err := c.clientset.CoreV1().RESTClient().
		Get().
		Resource("nodes").
		Name(nodeName).
		SubResource("proxy").
		Suffix("stats/summary").
		DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("querying stats/summary: %w", err)
	}

	var summary statsSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		return nil, fmt.Errorf("parsing stats/summary: %w", err)
	}

	result := make(map[types.NamespacedName]*VolumeStats)
	for _, pod := range summary.Pods {
		for _, vol := range pod.Volume {
			if vol.PVCRef == nil || vol.AvailableBytes == nil || vol.CapacityBytes == nil {
				continue
			}
			key := types.NamespacedName{
				Namespace: vol.PVCRef.Namespace,
				Name:      vol.PVCRef.Name,
			}
			result[key] = &VolumeStats{
				AvailableBytes: *vol.AvailableBytes,
				CapacityBytes:  *vol.CapacityBytes,
			}
		}
	}
	return result, nil
}
