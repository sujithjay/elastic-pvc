package controller

import (
	"context"
	"sort"
	"testing"
	"time"

	elasticpvc "elastic-pvc"
	"elastic-pvc/internal/kubelet"
	"elastic-pvc/internal/resize"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestIsTargetPVC(t *testing.T) {
	fsMode := corev1.PersistentVolumeFilesystem
	blockMode := corev1.PersistentVolumeBlock

	tests := []struct {
		name string
		pvc  *corev1.PersistentVolumeClaim
		want bool
	}{
		{
			"valid target",
			&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						elasticpvc.StorageLimitAnnotation: "100Gi",
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeMode: &fsMode,
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			},
			true,
		},
		{
			"no storage-limit annotation",
			&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			},
			false,
		},
		{
			"block mode",
			&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						elasticpvc.StorageLimitAnnotation: "100Gi",
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					VolumeMode: &blockMode,
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			},
			false,
		},
		{
			"pending PVC",
			&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						elasticpvc.StorageLimitAnnotation: "100Gi",
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimPending,
				},
			},
			false,
		},
		{
			"nil volume mode defaults to filesystem",
			&corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						elasticpvc.StorageLimitAnnotation: "100Gi",
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
				},
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTargetPVC(tt.pvc)
			if got != tt.want {
				t.Errorf("isTargetPVC() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnnotationOrDefault(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				elasticpvc.ThresholdAnnotation: "30%",
			},
		},
	}

	if got := annotationOrDefault(pvc, elasticpvc.ThresholdAnnotation, "20%"); got != "30%" {
		t.Errorf("expected 30%%, got %s", got)
	}
	if got := annotationOrDefault(pvc, elasticpvc.IncreaseAnnotation, "50%"); got != "50%" {
		t.Errorf("expected default 50%%, got %s", got)
	}
}

// fakeMetricsClient implements kubelet.MetricsClient for testing.
type fakeMetricsClient struct {
	stats       map[types.NamespacedName]*kubelet.VolumeStats
	failedNodes []kubelet.NodeError
	err         error
}

func (f *fakeMetricsClient) GetMetrics(_ context.Context, _ []string) (*kubelet.MetricsResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &kubelet.MetricsResult{
		Stats:       f.stats,
		FailedNodes: f.failedNodes,
	}, nil
}

func TestResizeDecision(t *testing.T) {
	// Simulate the resize decision logic without a real k8s client.
	// This tests the threshold comparison and size calculation.
	const gib = 1 << 30

	tests := []struct {
		name         string
		currentCap   int64
		available    int64
		capacity     int64
		threshold    string
		increase     string
		limit        int64
		shouldResize bool
		expectedSize int64
	}{
		{
			"needs resize: 10% free, threshold 20%",
			100 * int64(gib), // currentCap
			10 * int64(gib),  // available
			100 * int64(gib), // capacity
			"20%",            // threshold
			"50%",            // increase
			500 * int64(gib), // limit
			true,
			150 * int64(gib), // 100 + 50% = 150
		},
		{
			"no resize needed: 30% free, threshold 20%",
			100 * int64(gib),
			30 * int64(gib),
			100 * int64(gib),
			"20%",
			"50%",
			500 * int64(gib),
			false,
			0,
		},
		{
			"resize capped at limit",
			90 * int64(gib),
			5 * int64(gib),
			90 * int64(gib),
			"20%",
			"50%",
			100 * int64(gib),
			true,
			100 * int64(gib), // 90 + 45 = 135, capped to 100
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thresholdBytes, err := resize.ParseValue(tt.threshold, tt.capacity)
			if err != nil {
				t.Fatalf("parsing threshold: %v", err)
			}

			shouldResize := tt.available < thresholdBytes
			if shouldResize != tt.shouldResize {
				t.Fatalf("shouldResize = %v, want %v (available=%d, threshold=%d)",
					shouldResize, tt.shouldResize, tt.available, thresholdBytes)
			}

			if !shouldResize {
				return
			}

			increaseBytes, err := resize.ParseValue(tt.increase, tt.currentCap)
			if err != nil {
				t.Fatalf("parsing increase: %v", err)
			}

			newSize := resize.CalculateNewSize(tt.currentCap, increaseBytes, tt.limit)
			if newSize != tt.expectedSize {
				t.Errorf("newSize = %d (%s), want %d (%s)",
					newSize, resource.NewQuantity(newSize, resource.BinarySI).String(),
					tt.expectedSize, resource.NewQuantity(tt.expectedSize, resource.BinarySI).String())
			}
		})
	}
}

func TestNodesForPods(t *testing.T) {
	tests := []struct {
		name       string
		pods       []corev1.Pod
		targetPVCs map[string]struct{}
		wantNodes  map[string]struct{}
	}{
		{
			"pods mounting target PVCs return their nodes",
			[]corev1.Pod{
				{Spec: corev1.PodSpec{
					NodeName: "node-1",
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-a"},
						},
					}},
				}},
			},
			map[string]struct{}{"pvc-a": {}},
			map[string]struct{}{"node-1": {}},
		},
		{
			"pods mounting non-target PVCs are excluded",
			[]corev1.Pod{
				{Spec: corev1.PodSpec{
					NodeName: "node-1",
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "other-pvc"},
						},
					}},
				}},
			},
			map[string]struct{}{"pvc-a": {}},
			map[string]struct{}{},
		},
		{
			"unscheduled pods (no NodeName) are skipped",
			[]corev1.Pod{
				{Spec: corev1.PodSpec{
					NodeName: "",
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-a"},
						},
					}},
				}},
			},
			map[string]struct{}{"pvc-a": {}},
			map[string]struct{}{},
		},
		{
			"multiple pods on same node are deduplicated",
			[]corev1.Pod{
				{Spec: corev1.PodSpec{
					NodeName: "node-1",
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-a"},
						},
					}},
				}},
				{Spec: corev1.PodSpec{
					NodeName: "node-1",
					Volumes: []corev1.Volume{{
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-b"},
						},
					}},
				}},
			},
			map[string]struct{}{"pvc-a": {}, "pvc-b": {}},
			map[string]struct{}{"node-1": {}},
		},
		{
			"empty inputs return empty result",
			nil,
			map[string]struct{}{},
			map[string]struct{}{},
		},
		{
			"pod mounting multiple PVCs, one target",
			[]corev1.Pod{
				{Spec: corev1.PodSpec{
					NodeName: "node-2",
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"},
								},
							},
						},
						{
							Name: "other",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "non-target"},
							},
						},
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-a"},
							},
						},
					},
				}},
			},
			map[string]struct{}{"pvc-a": {}},
			map[string]struct{}{"node-2": {}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nodesForPods(tt.pods, tt.targetPVCs)
			if len(got) != len(tt.wantNodes) {
				t.Fatalf("nodesForPods() returned %d nodes, want %d", len(got), len(tt.wantNodes))
			}
			for node := range tt.wantNodes {
				if _, ok := got[node]; !ok {
					t.Errorf("nodesForPods() missing expected node %q", node)
				}
			}
		})
	}
}

func TestIsInCooldown(t *testing.T) {
	defaultCooldown := 5 * time.Minute

	tests := []struct {
		name        string
		annotations map[string]string
		wantResult  bool
	}{
		{
			"no last-resize annotation",
			map[string]string{},
			false,
		},
		{
			"last-resize was 10 minutes ago (past cooldown)",
			map[string]string{
				elasticpvc.LastResizeAnnotation: time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
			},
			false,
		},
		{
			"last-resize was 2 minutes ago (within cooldown)",
			map[string]string{
				elasticpvc.LastResizeAnnotation: time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339),
			},
			true,
		},
		{
			"last-resize was exactly at cooldown boundary",
			map[string]string{
				elasticpvc.LastResizeAnnotation: time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339),
			},
			false, // at boundary should not be in cooldown
		},
		{
			"invalid timestamp format",
			map[string]string{
				elasticpvc.LastResizeAnnotation: "invalid-timestamp",
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Autoscaler{
				resizeCooldown: defaultCooldown,
			}
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}
			got := a.isInCooldown(pvc)
			if got != tt.wantResult {
				t.Errorf("isInCooldown() = %v, want %v", got, tt.wantResult)
			}
		})
	}
}

func TestGetCooldown(t *testing.T) {
	defaultCooldown := 5 * time.Minute

	tests := []struct {
		name        string
		annotations map[string]string
		want        time.Duration
	}{
		{
			"no cooldown annotation, uses default",
			map[string]string{},
			defaultCooldown,
		},
		{
			"valid cooldown annotation override",
			map[string]string{
				elasticpvc.CooldownAnnotation: "10m",
			},
			10 * time.Minute,
		},
		{
			"cooldown annotation with hours",
			map[string]string{
				elasticpvc.CooldownAnnotation: "1h",
			},
			1 * time.Hour,
		},
		{
			"cooldown annotation with seconds",
			map[string]string{
				elasticpvc.CooldownAnnotation: "30s",
			},
			30 * time.Second,
		},
		{
			"invalid cooldown annotation, falls back to default",
			map[string]string{
				elasticpvc.CooldownAnnotation: "invalid",
			},
			defaultCooldown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Autoscaler{
				resizeCooldown: defaultCooldown,
			}
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}
			got := a.getCooldown(pvc)
			if got != tt.want {
				t.Errorf("getCooldown() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCandidatePrioritization(t *testing.T) {
	// Test that candidates are sorted by availableBytes ascending (most critical first)
	candidates := []*resizeCandidate{
		{
			pvc:            &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-high"}},
			availableBytes: 100 * 1024 * 1024, // 100MB
		},
		{
			pvc:            &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-critical"}},
			availableBytes: 10 * 1024 * 1024, // 10MB - most critical
		},
		{
			pvc:            &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-medium"}},
			availableBytes: 50 * 1024 * 1024, // 50MB
		},
	}

	// Sort like reconcile() does
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].availableBytes < candidates[j].availableBytes
	})

	// Verify order: critical, medium, high
	expectedOrder := []string{"pvc-critical", "pvc-medium", "pvc-high"}
	for i, expected := range expectedOrder {
		if candidates[i].pvc.Name != expected {
			t.Errorf("position %d: got %s, want %s", i, candidates[i].pvc.Name, expected)
		}
	}
}

func TestPerCycleLimit(t *testing.T) {
	// Test that only maxResizesPerCycle candidates would be processed
	maxResizes := 2
	candidates := []*resizeCandidate{
		{pvc: &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-1"}}, availableBytes: 10},
		{pvc: &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-2"}}, availableBytes: 20},
		{pvc: &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-3"}}, availableBytes: 30},
		{pvc: &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pvc-4"}}, availableBytes: 40},
	}

	// Simulate the per-cycle limit logic from reconcile()
	var processed []string
	var deferred []string

	resizeCount := 0
	for _, candidate := range candidates {
		if resizeCount >= maxResizes {
			deferred = append(deferred, candidate.pvc.Name)
			continue
		}
		processed = append(processed, candidate.pvc.Name)
		resizeCount++
	}

	if len(processed) != maxResizes {
		t.Errorf("processed %d candidates, want %d", len(processed), maxResizes)
	}

	if len(deferred) != len(candidates)-maxResizes {
		t.Errorf("deferred %d candidates, want %d", len(deferred), len(candidates)-maxResizes)
	}

	// Verify the right candidates were processed (first two by available bytes)
	expectedProcessed := []string{"pvc-1", "pvc-2"}
	for i, expected := range expectedProcessed {
		if processed[i] != expected {
			t.Errorf("processed[%d] = %s, want %s", i, processed[i], expected)
		}
	}

	// Verify the right candidates were deferred
	expectedDeferred := []string{"pvc-3", "pvc-4"}
	for i, expected := range expectedDeferred {
		if deferred[i] != expected {
			t.Errorf("deferred[%d] = %s, want %s", i, deferred[i], expected)
		}
	}
}
