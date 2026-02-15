#!/usr/bin/env bash
# Setup script for elastic-pvc KIND testing environment
# Creates a KIND cluster with CSI Hostpath Driver for volume expansion testing

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CLUSTER_NAME="elastic-pvc-test"
CSI_DRIVER_VERSION="v1.15.0"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

check_dependencies() {
    log_info "Checking dependencies..."
    local missing=()

    for cmd in kind kubectl helm docker; do
        if ! command -v "$cmd" &> /dev/null; then
            missing+=("$cmd")
        fi
    done

    if [ ${#missing[@]} -ne 0 ]; then
        log_error "Missing required tools: ${missing[*]}"
        exit 1
    fi

    log_info "All dependencies found"
}

create_cluster() {
    log_info "Creating KIND cluster '${CLUSTER_NAME}'..."

    # Check if cluster already exists
    if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
        log_warn "Cluster '${CLUSTER_NAME}' already exists. Delete it first with teardown.sh"
        exit 1
    fi

    # Create host path for local storage
    mkdir -p /tmp/elastic-pvc-test

    kind create cluster --config "${SCRIPT_DIR}/kind-config.yaml"

    log_info "Waiting for cluster to be ready..."
    kubectl wait --for=condition=Ready nodes --all --timeout=60s
}

deploy_csi_hostpath_driver() {
    log_info "Deploying CSI Hostpath Driver..."

    # Apply RBAC for CSI sidecars (provisioner, attacher, resizer, snapshotter)
    # These are required for the CSI driver to work properly
    local RBAC_BASE="https://raw.githubusercontent.com/kubernetes-csi"

    log_info "Applying CSI sidecar RBAC..."
    kubectl apply -f "${RBAC_BASE}/external-provisioner/v5.0.1/deploy/kubernetes/rbac.yaml"
    kubectl apply -f "${RBAC_BASE}/external-attacher/v4.6.1/deploy/kubernetes/rbac.yaml"
    kubectl apply -f "${RBAC_BASE}/external-resizer/v1.11.1/deploy/kubernetes/rbac.yaml"
    kubectl apply -f "${RBAC_BASE}/external-snapshotter/v8.0.1/deploy/kubernetes/csi-snapshotter/rbac-csi-snapshotter.yaml"
    kubectl apply -f "${RBAC_BASE}/external-health-monitor/v0.12.1/deploy/kubernetes/external-health-monitor-controller/rbac.yaml"

    # Apply the CSI hostpath driver
    log_info "Applying CSI hostpath driver..."
    kubectl apply -f "https://raw.githubusercontent.com/kubernetes-csi/csi-driver-host-path/${CSI_DRIVER_VERSION}/deploy/kubernetes-1.27/hostpath/csi-hostpath-driverinfo.yaml"
    kubectl apply -f "https://raw.githubusercontent.com/kubernetes-csi/csi-driver-host-path/${CSI_DRIVER_VERSION}/deploy/kubernetes-1.27/hostpath/csi-hostpath-plugin.yaml"

    log_info "Waiting for CSI driver pods to be ready..."
    kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=csi-hostpathplugin -n default --timeout=180s || {
        log_warn "CSI driver pods not ready yet, checking status..."
        kubectl get pods -l app.kubernetes.io/name=csi-hostpathplugin
        sleep 30
        kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=csi-hostpathplugin -n default --timeout=120s
    }
}

create_storage_class() {
    log_info "Creating StorageClass with elastic-pvc enabled..."

    kubectl apply -f - <<EOF
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: elastic-pvc-test
  annotations:
    elastic-pvc.io/enabled: "true"
provisioner: hostpath.csi.k8s.io
allowVolumeExpansion: true
volumeBindingMode: Immediate
reclaimPolicy: Delete
EOF

    log_info "StorageClass 'elastic-pvc-test' created"
}

build_and_load_image() {
    log_info "Building controller image..."
    cd "${PROJECT_ROOT}"

    docker build -t elastic-pvc:latest .

    log_info "Loading image into KIND cluster..."
    kind load docker-image elastic-pvc:latest --name "${CLUSTER_NAME}"
}

deploy_controller() {
    log_info "Deploying elastic-pvc controller via Helm..."

    helm upgrade --install elastic-pvc "${PROJECT_ROOT}/deploy/helm/elastic-pvc" \
        --set image.repository=elastic-pvc \
        --set image.tag=latest \
        --set image.pullPolicy=Never \
        --set controller.interval=10s \
        --set controller.logLevel=debug \
        --set controller.cooldown=30s \
        --wait

    log_info "Waiting for controller to be ready..."
    kubectl wait --for=condition=Ready pod -l app.kubernetes.io/name=elastic-pvc --timeout=60s
}

main() {
    log_info "Setting up elastic-pvc test environment"

    check_dependencies
    create_cluster
    deploy_csi_hostpath_driver
    create_storage_class
    build_and_load_image
    deploy_controller

    echo ""
    log_info "Setup complete!"
    echo ""
    echo "Next steps:"
    echo "  1. Deploy test workload:  ./deploy-test-workload.sh"
    echo "  2. Trigger resize test:   ./test-resize.sh"
    echo "  3. Watch controller logs: kubectl logs -f -l app.kubernetes.io/name=elastic-pvc"
    echo "  4. Cleanup:               ./teardown.sh"
}

main "$@"
