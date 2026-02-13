package controller

import (
	"context"

	elasticpvc "elastic-pvc"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	indexResizeEnabled    = ".metadata.annotations[elastic-pvc.io/enabled]"
	indexStorageClassName = ".spec.storageClassName"
)

// SetupIndexer creates field indexes on StorageClass and PVC objects
// so the reconciler can efficiently query them.
func SetupIndexer(mgr ctrl.Manager) error {
	err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&storagev1.StorageClass{},
		indexResizeEnabled,
		indexByResizeEnabled,
	)
	if err != nil {
		return err
	}

	return mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&corev1.PersistentVolumeClaim{},
		indexStorageClassName,
		indexByStorageClassName,
	)
}

func indexByResizeEnabled(obj client.Object) []string {
	sc := obj.(*storagev1.StorageClass)
	if val, ok := sc.Annotations[elasticpvc.AutoResizeEnabledKey]; ok {
		return []string{val}
	}
	return []string{}
}

func indexByStorageClassName(obj client.Object) []string {
	pvc := obj.(*corev1.PersistentVolumeClaim)
	if pvc.Spec.StorageClassName == nil {
		return []string{}
	}
	return []string{*pvc.Spec.StorageClassName}
}
