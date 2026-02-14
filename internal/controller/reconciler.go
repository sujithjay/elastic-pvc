package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	elasticpvc "elastic-pvc"
	"elastic-pvc/internal/kubelet"
	"elastic-pvc/internal/resize"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Autoscaler periodically checks PVC usage and expands volumes when needed.
type Autoscaler struct {
	metricsClient kubelet.MetricsClient
	client        client.Client
	recorder      record.EventRecorder
	interval      time.Duration
	log           logr.Logger
}

// NewAutoscaler creates an Autoscaler that runs as a controller-runtime Runnable.
func NewAutoscaler(mc kubelet.MetricsClient, c client.Client, recorder record.EventRecorder, interval time.Duration) *Autoscaler {
	return &Autoscaler{
		metricsClient: mc,
		client:        c,
		recorder:      recorder,
		interval:      interval,
		log:           ctrl.Log.WithName("autoscaler"),
	}
}

// Start implements manager.Runnable. It runs the resize check loop until the
// context is cancelled.
func (a *Autoscaler) Start(ctx context.Context) error {
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.reconcile(ctx)
		}
	}
}

func (a *Autoscaler) reconcile(ctx context.Context) {
	scList, err := a.getEnabledStorageClasses(ctx)
	if err != nil {
		a.log.Error(err, "listing storage classes")
		return
	}

	nodeNames, err := a.getNodesWithTargetPVCs(ctx, scList)
	if err != nil {
		a.log.Error(err, "getting nodes with target PVCs")
		return
	}

	if len(nodeNames) == 0 {
		a.log.V(1).Info("no target PVCs found, skipping kubelet queries")
		return
	}

	a.log.V(1).Info("querying kubelet stats", "nodeCount", len(nodeNames))

	vsMap, err := a.metricsClient.GetMetrics(ctx, nodeNames)
	if err != nil {
		a.log.Error(err, "getting volume metrics")
		return
	}

	for _, sc := range scList.Items {
		a.reconcileStorageClass(ctx, &sc, vsMap)
	}
}

func (a *Autoscaler) getEnabledStorageClasses(ctx context.Context) (*storagev1.StorageClassList, error) {
	var scList storagev1.StorageClassList
	err := a.client.List(ctx, &scList, client.MatchingFields{
		indexResizeEnabled: "true",
	})
	if err != nil {
		return nil, err
	}
	return &scList, nil
}

func (a *Autoscaler) reconcileStorageClass(ctx context.Context, sc *storagev1.StorageClass, vsMap map[types.NamespacedName]*kubelet.VolumeStats) {
	var pvcList corev1.PersistentVolumeClaimList
	err := a.client.List(ctx, &pvcList, client.MatchingFields{
		indexStorageClassName: sc.Name,
	})
	if err != nil {
		a.log.Error(err, "listing PVCs", "storageClass", sc.Name)
		return
	}

	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		if !isTargetPVC(pvc) {
			continue
		}

		key := types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name}
		vs, ok := vsMap[key]
		if !ok {
			continue // volume not mounted by any pod
		}

		if err := a.resizeIfNeeded(ctx, pvc, vs); err != nil {
			a.log.Error(err, "resizing PVC", "namespace", pvc.Namespace, "name", pvc.Name)
		}
	}
}

// isTargetPVC returns true if the PVC should be managed by elastic-pvc.
func isTargetPVC(pvc *corev1.PersistentVolumeClaim) bool {
	if _, ok := pvc.Annotations[elasticpvc.StorageLimitAnnotation]; !ok {
		return false
	}
	if pvc.Spec.VolumeMode != nil && *pvc.Spec.VolumeMode != corev1.PersistentVolumeFilesystem {
		return false
	}
	return pvc.Status.Phase == corev1.ClaimBound
}

// getNodesWithTargetPVCs returns the set of node names where target PVCs are mounted.
func (a *Autoscaler) getNodesWithTargetPVCs(ctx context.Context, scList *storagev1.StorageClassList) ([]string, error) {
	nodeSet := make(map[string]struct{})

	for _, sc := range scList.Items {
		var pvcList corev1.PersistentVolumeClaimList
		if err := a.client.List(ctx, &pvcList, client.MatchingFields{
			indexStorageClassName: sc.Name,
		}); err != nil {
			return nil, fmt.Errorf("listing PVCs for StorageClass %s: %w", sc.Name, err)
		}

		for i := range pvcList.Items {
			pvc := &pvcList.Items[i]
			if !isTargetPVC(pvc) {
				continue
			}

			var podList corev1.PodList
			if err := a.client.List(ctx, &podList,
				client.InNamespace(pvc.Namespace),
			); err != nil {
				return nil, fmt.Errorf("listing pods in namespace %s: %w", pvc.Namespace, err)
			}

			for j := range podList.Items {
				pod := &podList.Items[j]
				if pod.Spec.NodeName == "" {
					continue
				}
				if podMountsPVC(pod, pvc.Name) {
					nodeSet[pod.Spec.NodeName] = struct{}{}
				}
			}
		}
	}

	nodes := make([]string, 0, len(nodeSet))
	for node := range nodeSet {
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// podMountsPVC returns true if the pod has a volume that references the named PVC.
func podMountsPVC(pod *corev1.Pod, pvcName string) bool {
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ClaimName == pvcName {
			return true
		}
	}
	return false
}

func (a *Autoscaler) resizeIfNeeded(ctx context.Context, pvc *corev1.PersistentVolumeClaim, vs *kubelet.VolumeStats) error {
	log := a.log.WithValues("namespace", pvc.Namespace, "name", pvc.Name)

	limitQty, err := resource.ParseQuantity(pvc.Annotations[elasticpvc.StorageLimitAnnotation])
	if err != nil {
		return fmt.Errorf("parsing storage-limit: %w", err)
	}
	limitBytes := limitQty.Value()

	currentCap, ok := pvc.Status.Capacity[corev1.ResourceStorage]
	if !ok || currentCap.Value() == 0 {
		log.V(1).Info("skipping: capacity not set yet")
		return nil
	}

	if currentCap.Value() >= limitBytes {
		log.V(1).Info("skipping: storage limit reached")
		return nil
	}

	// Detect in-progress resize
	if prevCap, ok := pvc.Annotations[elasticpvc.PrevCapacityAnnotation]; ok {
		if prevCapBytes, err := strconv.ParseInt(prevCap, 10, 64); err == nil {
			if prevCapBytes == vs.CapacityBytes {
				log.V(1).Info("skipping: resize in progress")
				return nil
			}
		}
	}

	// Parse threshold
	thresholdStr := annotationOrDefault(pvc, elasticpvc.ThresholdAnnotation, elasticpvc.DefaultThreshold)
	thresholdBytes, err := resize.ParseValue(thresholdStr, vs.CapacityBytes)
	if err != nil {
		return fmt.Errorf("parsing threshold %q: %w", thresholdStr, err)
	}

	if vs.AvailableBytes >= thresholdBytes {
		return nil // enough free space
	}

	// Parse increase
	increaseStr := annotationOrDefault(pvc, elasticpvc.IncreaseAnnotation, elasticpvc.DefaultIncrease)
	increaseBytes, err := resize.ParseValue(increaseStr, currentCap.Value())
	if err != nil {
		return fmt.Errorf("parsing increase %q: %w", increaseStr, err)
	}

	newSizeBytes := resize.CalculateNewSize(currentCap.Value(), increaseBytes, limitBytes)
	newSize := resource.NewQuantity(newSizeBytes, resource.BinarySI)

	// Patch the PVC
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}
	pvc.Annotations[elasticpvc.PrevCapacityAnnotation] = strconv.FormatInt(vs.CapacityBytes, 10)
	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = *newSize

	if err := a.client.Update(ctx, pvc); err != nil {
		return fmt.Errorf("updating PVC: %w", err)
	}

	log.Info("resize triggered",
		"from", currentCap.String(),
		"to", newSize.String(),
		"available", vs.AvailableBytes,
		"threshold", thresholdBytes,
	)
	a.recorder.Eventf(pvc, corev1.EventTypeNormal, "Resized",
		"PVC resized from %s to %s", currentCap.String(), newSize.String())

	return nil
}

func annotationOrDefault(pvc *corev1.PersistentVolumeClaim, key, defaultVal string) string {
	if val, ok := pvc.Annotations[key]; ok && val != "" {
		return val
	}
	return defaultVal
}
