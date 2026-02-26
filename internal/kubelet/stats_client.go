package kubelet

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

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
	clientset      kubernetes.Interface
	maxConcurrent  int
	nodeTimeout    time.Duration
}

// NewStatsClient creates a MetricsClient that reads volume stats from kubelet.
// maxConcurrent limits the number of parallel kubelet queries.
// nodeTimeout is the per-node query deadline.
func NewStatsClient(clientset kubernetes.Interface, maxConcurrent int, nodeTimeout time.Duration) MetricsClient {
	return &statsClient{
		clientset:     clientset,
		maxConcurrent: maxConcurrent,
		nodeTimeout:   nodeTimeout,
	}
}

func (c *statsClient) GetMetrics(ctx context.Context, nodeNames []string) (*MetricsResult, error) {
	var nodesToQuery []string

	if len(nodeNames) > 0 {
		nodesToQuery = nodeNames
	} else {
		nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("listing nodes: %w", err)
		}
		nodesToQuery = make([]string, len(nodes.Items))
		for i, node := range nodes.Items {
			nodesToQuery[i] = node.Name
		}
	}

	result := &MetricsResult{
		Stats: make(map[types.NamespacedName]*VolumeStats),
	}
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, c.maxConcurrent)

dispatch:
	for _, nodeName := range nodesToQuery {
		select {
		case <-ctx.Done():
			break dispatch
		case sem <- struct{}{}:
		}

		nodeName := nodeName
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			nodeCtx, cancel := context.WithTimeout(ctx, c.nodeTimeout)
			defer cancel()

			nodeStats, err := c.getNodeStats(nodeCtx, nodeName)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.FailedNodes = append(result.FailedNodes, NodeError{
					NodeName: nodeName,
					Err:      err,
				})
				return
			}
			for k, v := range nodeStats {
				result.Stats[k] = v
			}
		}()
	}

	wg.Wait()
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
