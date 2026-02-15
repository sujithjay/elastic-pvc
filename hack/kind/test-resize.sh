#!/usr/bin/env bash
# Test resize functionality by filling disk to trigger threshold
# The PVC is configured with 50% threshold, so we fill ~60% to trigger resize

set -euo pipefail

NAMESPACE="test-elastic-pvc"
POD_NAME="test-pod"
FILL_PERCENT="${1:-60}"  # Default to 60% fill

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

show_pvc_status() {
    echo ""
    echo "PVC Status:"
    kubectl get pvc -n "${NAMESPACE}" -o wide
    echo ""
    echo "PVC Annotations:"
    kubectl get pvc test-pvc -n "${NAMESPACE}" -o jsonpath='{.metadata.annotations}' | jq . 2>/dev/null || \
        kubectl get pvc test-pvc -n "${NAMESPACE}" -o yaml | grep -A20 "annotations:"
    echo ""
}

show_disk_usage() {
    echo "Disk usage in pod:"
    kubectl exec -n "${NAMESPACE}" "${POD_NAME}" -- df -h /data
    echo ""
}

main() {
    log_info "Testing elastic-pvc resize functionality"

    # Verify pod exists
    if ! kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" &>/dev/null; then
        log_error "Pod '${POD_NAME}' not found. Run deploy-test-workload.sh first."
        exit 1
    fi

    echo ""
    log_info "Initial state:"
    show_pvc_status
    show_disk_usage

    # Calculate how much data to write to reach target fill percentage
    # PVC is 1Gi = 1024Mi, but actual usable space is slightly less due to filesystem overhead
    log_info "Filling disk to ~${FILL_PERCENT}%..."

    # Get actual available space and calculate target
    TOTAL_KB=$(kubectl exec -n "${NAMESPACE}" "${POD_NAME}" -- df /data | tail -1 | awk '{print $2}')
    TARGET_KB=$((TOTAL_KB * FILL_PERCENT / 100))

    log_info "Total space: ${TOTAL_KB}KB, Target fill: ${TARGET_KB}KB"

    # Write data in chunks to reach target
    kubectl exec -n "${NAMESPACE}" "${POD_NAME}" -- sh -c "
        cd /data
        # Remove any existing test data
        rm -f testfile* 2>/dev/null || true
        # Write data in 10MB chunks
        i=0
        while true; do
            used=\$(df /data | tail -1 | awk '{print \$3}')
            if [ \$used -ge ${TARGET_KB} ]; then
                break
            fi
            dd if=/dev/zero of=testfile\$i bs=1M count=10 2>/dev/null
            i=\$((i + 1))
            # Safety limit
            if [ \$i -gt 100 ]; then
                break
            fi
        done
        echo 'Done writing data'
    "

    echo ""
    log_info "After filling disk:"
    show_disk_usage

    echo ""
    log_info "Waiting for controller to detect and resize..."
    echo "Watch the controller logs in another terminal:"
    echo "  kubectl logs -f -l app.kubernetes.io/name=elastic-pvc"
    echo ""
    echo "Watch PVC size changes:"
    echo "  kubectl get pvc -n ${NAMESPACE} -w"
    echo ""

    # Wait a bit and show status
    log_info "Checking PVC status after 15 seconds..."
    sleep 15

    show_pvc_status

    # Check if resize was requested
    NEW_SIZE=$(kubectl get pvc test-pvc -n "${NAMESPACE}" -o jsonpath='{.spec.resources.requests.storage}')
    if [ "${NEW_SIZE}" != "1Gi" ]; then
        log_info "Resize triggered! New requested size: ${NEW_SIZE}"
    else
        log_warn "Resize not yet triggered. Check controller logs for details."
        echo ""
        echo "Controller logs (last 20 lines):"
        kubectl logs -l app.kubernetes.io/name=elastic-pvc --tail=20
    fi
}

main "$@"
