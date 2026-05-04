#!/usr/bin/env bash
# Run payload-processor e2e tests on a Kind cluster.
#
# Environment variables (all optional except E2E_IMAGE):
#   E2E_IMAGE         - Payload processor container image (required).
#   MANIFEST_PATH     - Path to the DeepSeek model server manifest.
#                       Defaults to test/testdata/deepseek-model-server.yaml.
#   KIND_CLUSTER_NAME - Name of the Kind cluster (default: pp-e2e).
#   USE_KIND          - Set to "false" to skip Kind create/image-load
#                       (assumes an existing cluster and pre-loaded images).
#   E2E_NS            - Kubernetes namespace for the e2e test (default: pp-e2e).
#   SKIP_BUILD        - Set to "true" to skip image build + kind load.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# Defaults
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-pp-e2e}"
USE_KIND="${USE_KIND:-true}"
MANIFEST_PATH="${MANIFEST_PATH:-${REPO_ROOT}/test/testdata/deepseek-model-server.yaml}"
E2E_NS="${E2E_NS:-pp-e2e}"
SKIP_BUILD="${SKIP_BUILD:-false}"

export E2E_IMAGE="${E2E_IMAGE:?E2E_IMAGE must be set}"
export E2E_SIM_IMAGE="${E2E_SIM_IMAGE:-ghcr.io/llm-d/llm-d-inference-sim:latest}"
export MANIFEST_PATH
export E2E_NS

# --- Kind -------------------------------------------------------------------

install_kind() {
  if command -v kind &>/dev/null; then
    echo "kind already installed: $(kind version)"
    return
  fi
  echo "Installing kind..."
  go install sigs.k8s.io/kind@latest
}

ensure_kind_cluster() {
  if kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"; then
    echo "Kind cluster '${KIND_CLUSTER_NAME}' already exists."
  else
    echo "Creating Kind cluster '${KIND_CLUSTER_NAME}'..."
    kind create cluster --name "${KIND_CLUSTER_NAME}" --wait 60s
  fi
  kind export kubeconfig --name "${KIND_CLUSTER_NAME}"
}

load_images() {
  if [[ "${SKIP_BUILD}" == "true" ]]; then
    echo "SKIP_BUILD=true; skipping image build."
  else
    echo "Building payload processor image..."
    make image-build-local
  fi

  echo "Loading image ${E2E_IMAGE} into Kind cluster..."
  kind load docker-image "${E2E_IMAGE}" --name "${KIND_CLUSTER_NAME}"
}

# --- Main --------------------------------------------------------------------

if [[ "${USE_KIND}" == "true" ]]; then
  install_kind
  ensure_kind_cluster
  load_images
fi

echo ""
echo "=== Running E2E tests ==="
echo "  E2E_IMAGE:     ${E2E_IMAGE}"
echo "  MANIFEST_PATH: ${MANIFEST_PATH}"
echo "  E2E_NS:        ${E2E_NS}"
echo ""

go test -tags e2e -v -timeout 20m ./test/e2e/ \
  -ginkgo.v \
  -ginkgo.no-color=false
