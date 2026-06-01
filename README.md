# llm-d-inference-payload-processor

[![CI](https://github.com/llm-d/llm-d-inference-payload-processor/actions/workflows/ci-pr-checks.yaml/badge.svg)](https://github.com/llm-d/llm-d-inference-payload-processor/actions/workflows/ci-pr-checks.yaml)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

> **Inference payload processor for llm-d.**

## Deployment

The payload processor can be deployed using either **Helm** or **Kustomize**.

### Helm

```bash
helm install payload-processor ./config/charts/payload-processor \
    --set provider.name=[gke|istio] \
    --set inferenceGateway.name=inference-gateway
```

See [config/charts/payload-processor/README.md](config/charts/payload-processor/README.md) for the full parameter reference.

### Kustomize

```bash
# No provider (Deployment + Service only)
kubectl kustomize config/kustomize/overlays/default | kubectl apply -f -

# Istio (adds EnvoyFilter + DestinationRule)
kubectl kustomize config/kustomize/overlays/istio | kubectl apply -f -

# GKE (adds GCPRoutingExtension + HealthCheckPolicy)
kubectl kustomize config/kustomize/overlays/gke | kubectl apply -f -
```

See [config/kustomize/README.md](config/kustomize/README.md) for customization options (namespace, image tag, custom config, multi-namespace RBAC).
