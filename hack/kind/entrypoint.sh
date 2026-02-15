#!/usr/bin/env bash
# Entrypoint for containerized KIND tests
# Orchestrates the full test cycle: setup -> test -> teardown

set -euo pipefail

SCRIPT_DIR="/workspace/hack/kind"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step() { echo -e "${CYAN}[STEP]${NC} $1"; }

# Cleanup function - always run on exit
cleanup() {
    local exit_code=$?
    echo ""
    log_step "Cleaning up..."

    # Always attempt teardown, even on failure
    "${SCRIPT_DIR}/teardown.sh" || true

    if [ $exit_code -eq 0 ]; then
        echo ""
        log_info "All tests passed!"
    else
        echo ""
        log_error "Tests failed with exit code: $exit_code"
    fi

    exit $exit_code
}

trap cleanup EXIT

main() {
    echo ""
    echo "========================================"
    echo "  elastic-pvc KIND Integration Tests"
    echo "========================================"
    echo ""

    # Verify Docker is accessible
    log_step "Verifying Docker access..."
    if ! docker info &>/dev/null; then
        log_error "Cannot connect to Docker daemon. Is /var/run/docker.sock mounted?"
        exit 1
    fi
    log_info "Docker is accessible"

    # Step 1: Setup cluster and deploy controller
    log_step "Setting up KIND cluster and deploying controller..."
    "${SCRIPT_DIR}/setup.sh"

    # Step 2: Deploy test workload
    log_step "Deploying test workload..."
    "${SCRIPT_DIR}/deploy-test-workload.sh"

    # Step 3: Run resize test
    log_step "Running resize test..."
    "${SCRIPT_DIR}/test-resize.sh"

    # Step 4: Verify controller is processing PVCs
    log_step "Verifying controller operation..."

    # Give controller time to process
    sleep 5

    # Check controller logs for evidence of proper operation
    CONTROLLER_LOGS=$(kubectl logs -l app.kubernetes.io/name=elastic-pvc --tail=50)

    # Verify controller is querying kubelet stats (proves it found the PVC and StorageClass)
    if echo "${CONTROLLER_LOGS}" | grep -q "querying kubelet stats"; then
        log_info "Controller is processing PVCs correctly"
        echo ""
        echo "Controller logs:"
        echo "${CONTROLLER_LOGS}"
        echo ""
        log_warn "NOTE: CSI hostpath driver doesn't create real volume size limits."
        log_warn "The controller works correctly but resize won't trigger because"
        log_warn "the kubelet reports the node's disk capacity (~58GB), not 1Gi."
        log_warn "For actual resize testing, use AWS EBS or another real CSI driver."
    else
        log_error "Controller not processing PVCs. Logs:"
        echo "${CONTROLLER_LOGS}"
        exit 1
    fi

    # Cleanup is handled by trap
}

main "$@"
