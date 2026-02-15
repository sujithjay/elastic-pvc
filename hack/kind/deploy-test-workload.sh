#!/usr/bin/env bash
# Deploy test workload for elastic-pvc testing
# Creates a PVC with elastic-pvc annotations and a pod that mounts it

set -euo pipefail

NAMESPACE="test-elastic-pvc"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

main() {
    log_info "Deploying test workload"

    # Create namespace
    log_info "Creating namespace '${NAMESPACE}'..."
    kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

    # Create PVC with elastic-pvc annotations
    log_info "Creating test PVC..."
    kubectl apply -n "${NAMESPACE}" -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: test-pvc
  annotations:
    # Maximum size the PVC can grow to
    elastic-pvc.io/storage-limit: "10Gi"
    # Trigger resize when 50% full (easier to trigger for testing)
    elastic-pvc.io/threshold: "50%"
    # Double the size on each resize
    elastic-pvc.io/increase: "100%"
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: elastic-pvc-test
  resources:
    requests:
      storage: 1Gi
EOF

    # Create Pod that mounts the PVC
    log_info "Creating test pod..."
    kubectl apply -n "${NAMESPACE}" -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  containers:
    - name: test
      image: busybox:1.36
      command: ["sleep", "infinity"]
      volumeMounts:
        - name: data
          mountPath: /data
      resources:
        requests:
          cpu: 10m
          memory: 16Mi
        limits:
          cpu: 100m
          memory: 64Mi
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: test-pvc
EOF

    log_info "Waiting for PVC to be bound..."
    if ! kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/test-pvc -n "${NAMESPACE}" --timeout=120s; then
        log_error "PVC failed to bind. Diagnostics:"
        echo ""
        echo "=== PVC Events ==="
        kubectl describe pvc test-pvc -n "${NAMESPACE}" | tail -20
        echo ""
        echo "=== StorageClasses ==="
        kubectl get sc
        echo ""
        echo "=== CSIDrivers ==="
        kubectl get csidrivers
        echo ""
        echo "=== Provisioner logs ==="
        kubectl logs -l app.kubernetes.io/name=csi-hostpathplugin -c csi-provisioner --tail=30
        exit 1
    fi

    log_info "Waiting for pod to be ready..."
    kubectl wait --for=condition=Ready pod/test-pod -n "${NAMESPACE}" --timeout=120s

    echo ""
    log_info "Test workload deployed!"
    echo ""
    echo "PVC Status:"
    kubectl get pvc -n "${NAMESPACE}"
    echo ""
    echo "Pod Status:"
    kubectl get pod -n "${NAMESPACE}"
    echo ""
    echo "Next steps:"
    echo "  1. Run test-resize.sh to fill the disk and trigger resize"
    echo "  2. Watch controller logs: kubectl logs -f -l app.kubernetes.io/name=elastic-pvc"
    echo "  3. Check PVC size: kubectl get pvc -n ${NAMESPACE} -w"
}

main "$@"
