package kubelet

import (
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/types"
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
