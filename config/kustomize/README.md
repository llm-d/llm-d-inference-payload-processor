# Kustomize Deployment

This directory provides [Kustomize](https://kustomize.io/) manifests for deploying the
Inference Payload Processor (IPP). It mirrors the same resources as the Helm chart at
`config/charts/payload-processor/` and is the recommended path for integrations such as
[llm-d-benchmark](https://github.com/llm-d/llm-d-benchmark) and GitOps workflows.

## Structure

```
config/kustomize/
├── base/                        # Core resources (provider-agnostic)
│   ├── kustomization.yaml
│   ├── deployment.yaml          # Deployment
│   ├── service.yaml             # ClusterIP Service on port 9004 (HTTP2)
│   ├── serviceaccount.yaml      # ServiceAccount
│   ├── rbac.yaml                # Role + RoleBinding (single-namespace)
│   └── configmap.yaml           # Default PayloadProcessorConfig
└── overlays/
    ├── default/                 # No provider — Deployment + Service only
    ├── istio/                   # Adds EnvoyFilter + DestinationRule
    └── gke/                     # Adds GCPRoutingExtension + HealthCheckPolicy
```

## Quick Start

### Prerequisites

- `kubectl` ≥ 1.24
- `kustomize` ≥ 5.0 (or the `kustomize` embedded in `kubectl`)
- A running Kubernetes cluster with an Inference Gateway deployed

### Deploy (no provider)

```bash
# Render to stdout
kubectl kustomize config/kustomize/overlays/default

# Apply directly
kubectl kustomize config/kustomize/overlays/default | kubectl apply -f -

# Or via make
make kustomize-deploy
```

### Deploy with Istio

```bash
kubectl kustomize config/kustomize/overlays/istio | kubectl apply -f -

# Or via make
make kustomize-deploy KUSTOMIZE_OVERLAY=istio
```

### Deploy with GKE

```bash
kubectl kustomize config/kustomize/overlays/gke | kubectl apply -f -

# Or via make
make kustomize-deploy KUSTOMIZE_OVERLAY=gke
```

### Undeploy

```bash
make kustomize-undeploy                        # default overlay
make kustomize-undeploy KUSTOMIZE_OVERLAY=istio
make kustomize-undeploy KUSTOMIZE_OVERLAY=gke
```

## Customization

### Change the target namespace

Edit the `namespace:` field in the overlay's `kustomization.yaml`:

```yaml
# config/kustomize/overlays/default/kustomization.yaml
namespace: my-namespace   # ← change this
```

Or patch it inline from the command line:

```bash
cd config/kustomize/overlays/default
kustomize edit set namespace my-namespace
```

> **Istio and GKE users:** The `cluster_name` in `overlays/istio/envoyfilter.yaml` and the
> `host` in `overlays/istio/destinationrule.yaml` embed the namespace as part of the FQDN
> (`payload-processor.<namespace>.svc.cluster.local`). Update those fields to match your
> chosen namespace.

### Change the container image

Add an `images` override in your overlay's `kustomization.yaml`:

```yaml
images:
  - name: ghcr.io/llm-d/llm-d-inference-payload-processor
    newTag: v0.3.0
```

### Change the Gateway name

Patch the `targetRefs[0].name` field in `envoyfilter.yaml` (Istio) or
`gcproutingextension.yaml` (GKE) using a strategic merge patch:

```yaml
# overlays/istio/gateway-patch.yaml
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: payload-processor
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: my-custom-gateway   # ← your Gateway name
```

```yaml
# overlays/istio/kustomization.yaml (add to existing file)
patches:
  - path: gateway-patch.yaml
```

### Use a custom IPP config

Add a `configMapGenerator` entry in your overlay's `kustomization.yaml` to merge your own
`PayloadProcessorConfig`:

```yaml
configMapGenerator:
  - name: payload-processor
    behavior: merge
    files:
      - custom-ipp-config.yaml=path/to/your/config.yaml
```

Then update the `--config-file` arg in a Deployment patch to point to
`/config/custom-ipp-config.yaml`.

### Multi-namespace RBAC

The base uses a namespace-scoped `Role`/`RoleBinding`. To watch ConfigMaps across
namespaces, create an overlay that replaces them with a `ClusterRole`/`ClusterRoleBinding`:

```yaml
# overlays/multi-namespace/kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: my-namespace

resources:
  - ../../base
  - clusterrole.yaml
  - clusterrolebinding.yaml

patches:
  - target:
      kind: Role
    patch: |-
      $patch: delete
      apiVersion: rbac.authorization.k8s.io/v1
      kind: Role
      metadata:
        name: payload-processor-configmap-reader
  - target:
      kind: RoleBinding
    patch: |-
      $patch: delete
      apiVersion: rbac.authorization.k8s.io/v1
      kind: RoleBinding
      metadata:
        name: payload-processor-configmap-reader
```

## Notes

- This chart should only be deployed once per Gateway (same constraint as the Helm chart).
- The `base/` layer intentionally omits `metadata.namespace` so that the overlay's
  `namespace:` field is the single source of truth.
- For production use, pin the image tag and consider setting resource requests/limits via
  a Deployment patch.
