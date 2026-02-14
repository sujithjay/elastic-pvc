package controller

import (
	"context"
	"testing"

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
	stats map[types.NamespacedName]*kubelet.VolumeStats
}

func (f *fakeMetricsClient) GetMetrics(_ context.Context, _ []string) (map[types.NamespacedName]*kubelet.VolumeStats, error) {
	return f.stats, nil
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

func TestPodMountsPVC(t *testing.T) {
	tests := []struct {
		name    string
		pod     *corev1.Pod
		pvcName string
		want    bool
	}{
		{
			"pod mounts the PVC",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "my-pvc",
								},
							},
						},
					},
				},
			},
			"my-pvc",
			true,
		},
		{
			"pod mounts different PVC",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "other-pvc",
								},
							},
						},
					},
				},
			},
			"my-pvc",
			false,
		},
		{
			"pod has no PVC volumes",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "my-config"},
								},
							},
						},
					},
				},
			},
			"my-pvc",
			false,
		},
		{
			"pod has no volumes",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{},
				},
			},
			"my-pvc",
			false,
		},
		{
			"pod mounts multiple volumes including target PVC",
			&corev1.Pod{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "my-config"},
								},
							},
						},
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "my-pvc",
								},
							},
						},
					},
				},
			},
			"my-pvc",
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := podMountsPVC(tt.pod, tt.pvcName)
			if got != tt.want {
				t.Errorf("podMountsPVC() = %v, want %v", got, tt.want)
			}
		})
	}
}
