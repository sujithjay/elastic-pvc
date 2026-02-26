package controller

import (
	"context"
	"fmt"
	"sort"
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
	metricsClient      kubelet.MetricsClient
	client             client.Client
	recorder           record.EventRecorder
	interval           time.Duration
	maxResizesPerCycle int
	resizeCooldown     time.Duration
	log                logr.Logger
}

// resizeCandidate represents a PVC that needs to be resized.
type resizeCandidate struct {
	pvc            *corev1.PersistentVolumeClaim
	stats          *kubelet.VolumeStats
	newSize        int64
	currentCapQty  resource.Quantity
	availableBytes int64
}

// NewAutoscaler creates an Autoscaler that runs as a controller-runtime Runnable.
func NewAutoscaler(mc kubelet.MetricsClient, c client.Client, recorder record.EventRecorder, interval time.Duration, maxResizesPerCycle int, resizeCooldown time.Duration) *Autoscaler {
	return &Autoscaler{
		metricsClient:      mc,
		client:             c,
		recorder:           recorder,
		interval:           interval,
		maxResizesPerCycle: maxResizesPerCycle,
		resizeCooldown:     resizeCooldown,
		log:                ctrl.Log.WithName("autoscaler"),
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
			a.runReconcile(ctx)
		}
	}
}

// runReconcile wraps reconcile with a cycle-level timeout and duration observation.
// The timeout is 80% of the configured interval to prevent cycles from overlapping.
func (a *Autoscaler) runReconcile(ctx context.Context) {
	cycleTimeout := time.Duration(float64(a.interval) * 0.8)
	cycleCtx, cancel := context.WithTimeout(ctx, cycleTimeout)
	defer cancel()

	start := time.Now()
	a.reconcile(cycleCtx)
	reconcileDurationSeconds.Observe(time.Since(start).Seconds())
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

	metricsResult, err := a.metricsClient.GetMetrics(ctx, nodeNames)
	if err != nil {
		a.log.Error(err, "getting volume metrics")
		return
	}

	if metricsResult.HasFailures() {
		for _, nodeErr := range metricsResult.FailedNodes {
			a.log.Info("failed to query node kubelet stats",
				"node", nodeErr.NodeName,
				"error", nodeErr.Err.Error())
		}
		nodeQueryFailuresTotal.Add(float64(len(metricsResult.FailedNodes)))
	}

	vsMap := metricsResult.Stats

	// Collect candidates from all StorageClasses
	var allCandidates []*resizeCandidate
	for _, sc := range scList.Items {
		candidates := a.collectCandidates(ctx, &sc, vsMap)
		allCandidates = append(allCandidates, candidates...)
	}

	if len(allCandidates) == 0 {
		return
	}

	// Sort by available bytes ascending (most critical first)
	sort.Slice(allCandidates, func(i, j int) bool {
		return allCandidates[i].availableBytes < allCandidates[j].availableBytes
	})

	// Process up to maxResizesPerCycle candidates
	resizeCount := 0
	for _, candidate := range allCandidates {
		if resizeCount >= a.maxResizesPerCycle {
			a.log.V(1).Info("per-cycle limit reached, deferring resize",
				"namespace", candidate.pvc.Namespace,
				"name", candidate.pvc.Name,
				"availableBytes", candidate.availableBytes,
			)
			rateLimitedTotal.WithLabelValues("per_cycle_limit").Inc()
			continue
		}

		if err := a.executeResize(ctx, candidate); err != nil {
			a.log.Error(err, "resizing PVC",
				"namespace", candidate.pvc.Namespace,
				"name", candidate.pvc.Name,
			)
			continue
		}
		resizeCount++
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

// collectCandidates evaluates all PVCs in the StorageClass and returns resize candidates.
func (a *Autoscaler) collectCandidates(ctx context.Context, sc *storagev1.StorageClass, vsMap map[types.NamespacedName]*kubelet.VolumeStats) []*resizeCandidate {
	var pvcList corev1.PersistentVolumeClaimList
	err := a.client.List(ctx, &pvcList, client.MatchingFields{
		indexStorageClassName: sc.Name,
	})
	if err != nil {
		a.log.Error(err, "listing PVCs", "storageClass", sc.Name)
		return nil
	}

	var candidates []*resizeCandidate
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

		candidate := a.evaluateCandidate(pvc, vs)
		if candidate != nil {
			candidates = append(candidates, candidate)
		}
	}

	return candidates
}

// evaluateCandidate checks if a PVC needs resizing and returns a candidate, or nil if not.
func (a *Autoscaler) evaluateCandidate(pvc *corev1.PersistentVolumeClaim, vs *kubelet.VolumeStats) *resizeCandidate {
	log := a.log.WithValues("namespace", pvc.Namespace, "name", pvc.Name)

	// Check cooldown first (cheap check)
	if a.isInCooldown(pvc) {
		log.V(1).Info("skipping: in cooldown period")
		rateLimitedTotal.WithLabelValues("cooldown").Inc()
		return nil
	}

	limitQty, err := resource.ParseQuantity(pvc.Annotations[elasticpvc.StorageLimitAnnotation])
	if err != nil {
		log.Error(err, "parsing storage-limit")
		return nil
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
		log.Error(err, "parsing threshold", "threshold", thresholdStr)
		return nil
	}

	if vs.AvailableBytes >= thresholdBytes {
		return nil // enough free space
	}

	// Parse increase
	increaseStr := annotationOrDefault(pvc, elasticpvc.IncreaseAnnotation, elasticpvc.DefaultIncrease)
	increaseBytes, err := resize.ParseValue(increaseStr, currentCap.Value())
	if err != nil {
		log.Error(err, "parsing increase", "increase", increaseStr)
		return nil
	}

	newSizeBytes := resize.CalculateNewSize(currentCap.Value(), increaseBytes, limitBytes)

	return &resizeCandidate{
		pvc:            pvc,
		stats:          vs,
		newSize:        newSizeBytes,
		currentCapQty:  currentCap,
		availableBytes: vs.AvailableBytes,
	}
}

// executeResize performs the actual PVC resize operation.
func (a *Autoscaler) executeResize(ctx context.Context, candidate *resizeCandidate) error {
	pvc := candidate.pvc
	newSize := resource.NewQuantity(candidate.newSize, resource.BinarySI)

	// Patch the PVC
	if pvc.Annotations == nil {
		pvc.Annotations = make(map[string]string)
	}
	pvc.Annotations[elasticpvc.PrevCapacityAnnotation] = strconv.FormatInt(candidate.stats.CapacityBytes, 10)
	pvc.Annotations[elasticpvc.LastResizeAnnotation] = time.Now().UTC().Format(time.RFC3339)
	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = *newSize

	if err := a.client.Update(ctx, pvc); err != nil {
		return fmt.Errorf("updating PVC: %w", err)
	}

	a.log.Info("resize triggered",
		"namespace", pvc.Namespace,
		"name", pvc.Name,
		"from", candidate.currentCapQty.String(),
		"to", newSize.String(),
		"available", candidate.stats.AvailableBytes,
	)
	a.recorder.Eventf(pvc, corev1.EventTypeNormal, "Resized",
		"PVC resized from %s to %s", candidate.currentCapQty.String(), newSize.String())
	resizesTotal.Inc()

	return nil
}

// getCooldown returns the effective cooldown for a PVC, checking the annotation override first.
func (a *Autoscaler) getCooldown(pvc *corev1.PersistentVolumeClaim) time.Duration {
	if override, ok := pvc.Annotations[elasticpvc.CooldownAnnotation]; ok {
		if d, err := time.ParseDuration(override); err == nil {
			return d
		}
		// If parsing fails, fall through to default
		a.log.V(1).Info("invalid cooldown annotation, using default",
			"namespace", pvc.Namespace,
			"name", pvc.Name,
			"value", override,
		)
	}
	return a.resizeCooldown
}

// isInCooldown returns true if the PVC was resized too recently.
func (a *Autoscaler) isInCooldown(pvc *corev1.PersistentVolumeClaim) bool {
	lastResize, ok := pvc.Annotations[elasticpvc.LastResizeAnnotation]
	if !ok {
		return false
	}

	lastResizeTime, err := time.Parse(time.RFC3339, lastResize)
	if err != nil {
		return false
	}

	cooldown := a.getCooldown(pvc)
	return time.Since(lastResizeTime) < cooldown
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
// It groups target PVCs by namespace, then lists pods once per namespace to find
// which nodes mount them — avoiding O(PVCs × Pods) API calls.
func (a *Autoscaler) getNodesWithTargetPVCs(ctx context.Context, scList *storagev1.StorageClassList) ([]string, error) {
	// Phase 1: collect target PVC names grouped by namespace.
	nsPVCs := make(map[string]map[string]struct{})
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
			if nsPVCs[pvc.Namespace] == nil {
				nsPVCs[pvc.Namespace] = make(map[string]struct{})
			}
			nsPVCs[pvc.Namespace][pvc.Name] = struct{}{}
		}
	}

	// Phase 2: for each namespace, list pods once and find mounting nodes.
	nodeSet := make(map[string]struct{})
	for ns, targetPVCs := range nsPVCs {
		var podList corev1.PodList
		if err := a.client.List(ctx, &podList, client.InNamespace(ns)); err != nil {
			return nil, fmt.Errorf("listing pods in namespace %s: %w", ns, err)
		}
		for node := range nodesForPods(podList.Items, targetPVCs) {
			nodeSet[node] = struct{}{}
		}
	}

	nodes := make([]string, 0, len(nodeSet))
	for node := range nodeSet {
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// nodesForPods returns the set of node names for pods that mount any of the target PVCs.
// Pods without a NodeName (unscheduled) are skipped.
func nodesForPods(pods []corev1.Pod, targetPVCs map[string]struct{}) map[string]struct{} {
	nodes := make(map[string]struct{})
	for i := range pods {
		pod := &pods[i]
		if pod.Spec.NodeName == "" {
			continue
		}
		for _, vol := range pod.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil {
				if _, ok := targetPVCs[vol.PersistentVolumeClaim.ClaimName]; ok {
					nodes[pod.Spec.NodeName] = struct{}{}
					break
				}
			}
		}
	}
	return nodes
}

func annotationOrDefault(pvc *corev1.PersistentVolumeClaim, key, defaultVal string) string {
	if val, ok := pvc.Annotations[key]; ok && val != "" {
		return val
	}
	return defaultVal
}
