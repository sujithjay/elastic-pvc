#!/usr/bin/env bash
# Run KIND integration tests in a container
# This script is meant to be run from the project root directory
#
# Usage:
#   ./hack/kind/run-test.sh [--no-cache]
#
# Options:
#   --no-cache    Force rebuild of test container image

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
IMAGE_NAME="elastic-pvc-kind-test"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Parse arguments
NO_CACHE=""
for arg in "$@"; do
    case $arg in
        --no-cache)
            NO_CACHE="--no-cache"
            ;;
    esac
done

# Verify Docker is available
if ! command -v docker &>/dev/null; then
    log_error "Docker is required but not installed"
    exit 1
fi

# Verify Docker daemon is running
if ! docker info &>/dev/null; then
    log_error "Docker daemon is not running"
    exit 1
fi

cd "${PROJECT_ROOT}"

# Build test container image
log_info "Building test container image..."
docker build ${NO_CACHE} -f hack/kind/Dockerfile.test -t "${IMAGE_NAME}" .

# Run tests
log_info "Running KIND integration tests..."
docker run --rm \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "${PROJECT_ROOT}":/workspace \
    --network host \
    "${IMAGE_NAME}"

exit_code=$?

if [ $exit_code -eq 0 ]; then
    log_info "Tests completed successfully"
else
    log_error "Tests failed with exit code: $exit_code"
fi

exit $exit_code
