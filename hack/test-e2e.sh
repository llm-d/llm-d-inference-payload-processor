#!/usr/bin/env bash
# Run payload-processor e2e tests on a Kind cluster.
#
# Environment variables (all optional except E2E_IMAGE):
#   E2E_IMAGE         - Payload processor container image (required).
#   E2E_SIM_IMAGE     - Model server simulator image
#                       (default: ghcr.io/llm-d/llm-d-inference-sim:v0.9.0).
#   KIND_CLUSTER_NAME - Name of the Kind cluster (default: ipp-e2e).
#   USE_KIND          - Set to "false" to skip Kind create/image-load
#                       (assumes an existing cluster and pre-loaded images).
#   E2E_NS            - Kubernetes namespace for the e2e test (default: ipp-e2e).
#   SKIP_BUILD        - Set to "true" to skip image build + kind load.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# Defaults
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-ipp-e2e}"
USE_KIND="${USE_KIND:-true}"
E2E_NS="${E2E_NS:-ipp-e2e}"
SKIP_BUILD="${SKIP_BUILD:-false}"
E2E_PORT="${E2E_PORT:-30080}"
E2E_METRICS_PORT="${E2E_METRICS_PORT:-30090}"

export E2E_IMAGE="${E2E_IMAGE:?E2E_IMAGE must be set}"
export E2E_SIM_IMAGE="${E2E_SIM_IMAGE:-ghcr.io/llm-d/llm-d-inference-sim:v0.9.0}"
export E2E_NS E2E_PORT E2E_METRICS_PORT

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
    cat <<KINDEOF | kind create cluster --name "${KIND_CLUSTER_NAME}" --config - --wait 60s
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30080
    hostPort: ${E2E_PORT}
    protocol: TCP
  - containerPort: 30090
    hostPort: ${E2E_METRICS_PORT}
    protocol: TCP
KINDEOF
    KIND_CREATED=true
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

  echo "Pre-pulling simulator image..."
  docker pull "${E2E_SIM_IMAGE}"
  if ! kind load docker-image "${E2E_SIM_IMAGE}" --name "${KIND_CLUSTER_NAME}" 2>/dev/null; then
    echo "kind load failed; falling back to docker save | ctr import..."
    docker save "${E2E_SIM_IMAGE}" | \
      docker exec -i "${KIND_CLUSTER_NAME}-control-plane" \
        ctr --namespace=k8s.io images import -
  fi
}

# --- Main --------------------------------------------------------------------

if [[ "${USE_KIND}" == "true" ]]; then
  install_kind
  ensure_kind_cluster
  load_images
fi

cleanup_kind() {
  if [[ "${USE_KIND}" == "true" && "${KIND_CREATED:-false}" == "true" ]]; then
    echo "Cleaning up Kind cluster '${KIND_CLUSTER_NAME}'..."
    kind delete cluster --name "${KIND_CLUSTER_NAME}" || true
  fi
}

trap cleanup_kind EXIT

echo ""
echo "=== Running E2E tests ==="
echo "  E2E_IMAGE:     ${E2E_IMAGE}"
echo "  E2E_SIM_IMAGE: ${E2E_SIM_IMAGE}"
echo "  E2E_NS:        ${E2E_NS}"
echo ""

go test -tags e2e -v -timeout 20m ./test/e2e/ \
  -ginkgo.v \
  -ginkgo.no-color=false
