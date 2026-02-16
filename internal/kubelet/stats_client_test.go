package kubelet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestParseStatsSummary(t *testing.T) {
	raw := `{
		"pods": [
			{
				"podRef": {"name": "spark-exec-1", "namespace": "default"},
				"volume": [
					{
						"name": "spark-local",
						"pvcRef": {"name": "spark-local-exec-1", "namespace": "default"},
						"usedBytes": 5368709120,
						"capacityBytes": 10737418240,
						"availableBytes": 5368709120
					},
					{
						"name": "no-pvc-volume"
					}
				]
			},
			{
				"podRef": {"name": "pod-no-volumes", "namespace": "default"},
				"volume": []
			}
		]
	}`

	var summary statsSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(summary.Pods) != 2 {
		t.Fatalf("expected 2 pods, got %d", len(summary.Pods))
	}

	// Simulate the extraction logic from getNodeStats
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

	if len(result) != 1 {
		t.Fatalf("expected 1 PVC volume, got %d", len(result))
	}

	key := types.NamespacedName{Namespace: "default", Name: "spark-local-exec-1"}
	vs, ok := result[key]
	if !ok {
		t.Fatal("expected spark-local-exec-1 in results")
	}

	if vs.AvailableBytes != 5368709120 {
		t.Errorf("availableBytes = %d, want 5368709120", vs.AvailableBytes)
	}
	if vs.CapacityBytes != 10737418240 {
		t.Errorf("capacityBytes = %d, want 10737418240", vs.CapacityBytes)
	}
}

func TestParseStatsSummary_NilFields(t *testing.T) {
	// Volume with pvcRef but nil capacity/available should be skipped
	raw := `{
		"pods": [{
			"podRef": {"name": "p1", "namespace": "ns1"},
			"volume": [{
				"name": "v1",
				"pvcRef": {"name": "pvc1", "namespace": "ns1"},
				"usedBytes": 100
			}]
		}]
	}`

	var summary statsSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, pod := range summary.Pods {
		for _, vol := range pod.Volume {
			if vol.PVCRef == nil || vol.AvailableBytes == nil || vol.CapacityBytes == nil {
				continue
			}
			t.Fatal("should not reach here: volume with nil fields should be skipped")
		}
	}
}

// makeStatsResponse generates a kubelet stats/summary JSON response.
func makeStatsResponse(pvcName, namespace string, available, capacity int64) string {
	return fmt.Sprintf(`{
		"pods": [{
			"podRef": {"name": "test-pod", "namespace": "%s"},
			"volume": [{
				"name": "data",
				"pvcRef": {"name": "%s", "namespace": "%s"},
				"usedBytes": %d,
				"capacityBytes": %d,
				"availableBytes": %d
			}]
		}]
	}`, namespace, pvcName, namespace, capacity-available, capacity, available)
}

// setupTestServer creates a test server that responds to kubelet stats requests.
// nodeResponses maps node names to their response (nil value means return error).
func setupTestServer(t *testing.T, nodeResponses map[string]*string) (*httptest.Server, kubernetes.Interface) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// URL format: /api/v1/nodes/{node}/proxy/stats/summary
		parts := strings.Split(r.URL.Path, "/")
		var nodeName string
		for i, part := range parts {
			if part == "nodes" && i+1 < len(parts) {
				nodeName = parts[i+1]
				break
			}
		}

		if nodeName == "" {
			http.Error(w, "node not found in path", http.StatusBadRequest)
			return
		}

		resp, ok := nodeResponses[nodeName]
		if !ok || resp == nil {
			http.Error(w, "node unreachable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(*resp))
	}))

	config := &rest.Config{
		Host: server.URL,
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("creating clientset: %v", err)
	}

	return server, clientset
}

func TestGetMetrics_SingleNodeFails(t *testing.T) {
	node1Resp := makeStatsResponse("pvc-1", "default", 5<<30, 10<<30)
	nodeResponses := map[string]*string{
		"node-1": &node1Resp,
		"node-2": nil, // This node will fail
	}

	server, clientset := setupTestServer(t, nodeResponses)
	defer server.Close()

	client := NewStatsClient(clientset)
	result, err := client.GetMetrics(context.Background(), []string{"node-1", "node-2"})

	if err != nil {
		t.Fatalf("GetMetrics returned error: %v", err)
	}

	// Should have partial results from node-1
	if len(result.Stats) != 1 {
		t.Errorf("expected 1 stat, got %d", len(result.Stats))
	}

	key := types.NamespacedName{Namespace: "default", Name: "pvc-1"}
	if _, ok := result.Stats[key]; !ok {
		t.Error("expected stats for pvc-1")
	}

	// Should have one failed node
	if len(result.FailedNodes) != 1 {
		t.Errorf("expected 1 failed node, got %d", len(result.FailedNodes))
	}

	if result.FailedNodes[0].NodeName != "node-2" {
		t.Errorf("expected failed node node-2, got %s", result.FailedNodes[0].NodeName)
	}

	if !result.HasFailures() {
		t.Error("HasFailures() should return true")
	}
}

func TestGetMetrics_AllNodesFail(t *testing.T) {
	nodeResponses := map[string]*string{
		"node-1": nil,
		"node-2": nil,
		"node-3": nil,
	}

	server, clientset := setupTestServer(t, nodeResponses)
	defer server.Close()

	client := NewStatsClient(clientset)
	result, err := client.GetMetrics(context.Background(), []string{"node-1", "node-2", "node-3"})

	if err != nil {
		t.Fatalf("GetMetrics returned error: %v", err)
	}

	// Should have empty stats
	if len(result.Stats) != 0 {
		t.Errorf("expected 0 stats, got %d", len(result.Stats))
	}

	// Should have all three nodes as failed
	if len(result.FailedNodes) != 3 {
		t.Errorf("expected 3 failed nodes, got %d", len(result.FailedNodes))
	}

	if !result.HasFailures() {
		t.Error("HasFailures() should return true")
	}
}

func TestGetMetrics_MixedFailures(t *testing.T) {
	node1Resp := makeStatsResponse("pvc-1", "ns1", 5<<30, 10<<30)
	node3Resp := makeStatsResponse("pvc-3", "ns3", 3<<30, 8<<30)

	nodeResponses := map[string]*string{
		"node-1": &node1Resp,
		"node-2": nil, // fails
		"node-3": &node3Resp,
		"node-4": nil, // fails
	}

	server, clientset := setupTestServer(t, nodeResponses)
	defer server.Close()

	client := NewStatsClient(clientset)
	result, err := client.GetMetrics(context.Background(), []string{"node-1", "node-2", "node-3", "node-4"})

	if err != nil {
		t.Fatalf("GetMetrics returned error: %v", err)
	}

	// Should have stats from 2 successful nodes
	if len(result.Stats) != 2 {
		t.Errorf("expected 2 stats, got %d", len(result.Stats))
	}

	key1 := types.NamespacedName{Namespace: "ns1", Name: "pvc-1"}
	key3 := types.NamespacedName{Namespace: "ns3", Name: "pvc-3"}

	if _, ok := result.Stats[key1]; !ok {
		t.Error("expected stats for pvc-1")
	}
	if _, ok := result.Stats[key3]; !ok {
		t.Error("expected stats for pvc-3")
	}

	// Should have 2 failed nodes
	if len(result.FailedNodes) != 2 {
		t.Errorf("expected 2 failed nodes, got %d", len(result.FailedNodes))
	}

	// Verify the failed nodes (order may vary due to concurrency)
	failedNodeNames := make(map[string]bool)
	for _, ne := range result.FailedNodes {
		failedNodeNames[ne.NodeName] = true
	}

	if !failedNodeNames["node-2"] {
		t.Error("expected node-2 in failed nodes")
	}
	if !failedNodeNames["node-4"] {
		t.Error("expected node-4 in failed nodes")
	}
}

func TestGetMetrics_AllNodesSucceed(t *testing.T) {
	node1Resp := makeStatsResponse("pvc-1", "default", 5<<30, 10<<30)
	node2Resp := makeStatsResponse("pvc-2", "default", 3<<30, 8<<30)

	nodeResponses := map[string]*string{
		"node-1": &node1Resp,
		"node-2": &node2Resp,
	}

	server, clientset := setupTestServer(t, nodeResponses)
	defer server.Close()

	client := NewStatsClient(clientset)
	result, err := client.GetMetrics(context.Background(), []string{"node-1", "node-2"})

	if err != nil {
		t.Fatalf("GetMetrics returned error: %v", err)
	}

	// Should have stats from both nodes
	if len(result.Stats) != 2 {
		t.Errorf("expected 2 stats, got %d", len(result.Stats))
	}

	// Should have no failed nodes
	if len(result.FailedNodes) != 0 {
		t.Errorf("expected 0 failed nodes, got %d", len(result.FailedNodes))
	}

	if result.HasFailures() {
		t.Error("HasFailures() should return false")
	}
}
