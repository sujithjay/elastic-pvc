package elasticpvc

const (
	// AnnotationPrefix is the prefix for all elastic-pvc annotations.
	AnnotationPrefix = "elastic-pvc.io"

	// AutoResizeEnabledKey is the StorageClass annotation that enables elastic-pvc.
	AutoResizeEnabledKey = AnnotationPrefix + "/enabled"

	// StorageLimitAnnotation is the PVC annotation specifying the maximum volume size.
	// Required for a PVC to be managed by elastic-pvc.
	StorageLimitAnnotation = AnnotationPrefix + "/storage-limit"

	// ThresholdAnnotation is the PVC annotation specifying the free-space threshold
	// that triggers expansion. Can be a percentage (e.g., "20%") or absolute (e.g., "10Gi").
	ThresholdAnnotation = AnnotationPrefix + "/threshold"

	// IncreaseAnnotation is the PVC annotation specifying how much to grow by each time.
	// Can be a percentage of current capacity (e.g., "50%") or absolute (e.g., "20Gi").
	IncreaseAnnotation = AnnotationPrefix + "/increase"

	// PrevCapacityAnnotation is an internal annotation tracking previous capacity
	// to detect in-progress resizes. Set automatically by the controller.
	PrevCapacityAnnotation = AnnotationPrefix + "/prev-capacity"

	// DefaultThreshold is the default free-space threshold (20% of capacity).
	DefaultThreshold = "20%"

	// DefaultIncrease is the default increase amount (50% of current capacity).
	DefaultIncrease = "50%"
)
