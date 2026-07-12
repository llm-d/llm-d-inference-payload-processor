# E2E Tests

End-to-end tests for the `llm-d-inference-payload-processor`.

These tests deploy a complete stack on a
[Kind](https://kind.sigs.k8s.io/) cluster and validate the payload
processor's behaviour through an Envoy proxy against model-server
simulators.

## Architecture

```text
Go test (OpenAI SDK) ──► Envoy NodePort (ext_proc) ──► Payload Processor
                                    │                              │
                                    │  route by header            │ ConfigMap watch
                                    ▼                              ▼
                           Llama / DeepSeek                    K8s API
                           model-server sims
```

The Go test process sends requests directly to Envoy via a Kind
NodePort (default: localhost:30080, configurable via `E2E_PORT`) using
the OpenAI Go SDK. The payload
processor extracts the `model` field from the request body, resolves
adapter names to base models via ConfigMaps, and sets the
`X-Gateway-Base-Model-Name` header. Envoy routes the request to the
correct model-server cluster based on that header.

For testing on an external (non-Kind) cluster, set `K8S_CONTEXT` and
the test will use `kubectl port-forward` instead of NodePort.

## Manifest Structure

Kubernetes manifests live under `deploy/` and are shared between
e2e tests and local Kind development:

```text
deploy/
  components/
    ipp/               RBAC, Deployment, Service for the payload processor
    model-server/
      llama/           Llama simulator + adapter ConfigMap
      deepseek/        DeepSeek simulator + adapter ConfigMap
  environments/
    dev/
      e2e-infra/       Envoy proxy config (NodePort service)
hack/
  test-e2e.sh          Orchestration script (Kind + build + test + cleanup)
```

Test code loads these manifests via relative paths and substitutes
`${NAMESPACE}`, `${IPP_IMAGE}`, and `${SIM_IMAGE}` at runtime.

## Prerequisites

- Go (version in `go.mod`)
- Docker
- [Kind](https://kind.sigs.k8s.io/)
  (`go install sigs.k8s.io/kind@latest` — installed automatically by `make test-e2e`)

## Quick Start

The simplest way to run the e2e tests from the repo root:

```bash
make test-e2e
```

This will:

1. Install Kind (if not already present).
2. Create a Kind cluster named `ipp-e2e` with NodePort mappings.
3. Build the payload-processor container image.
4. Load images into the cluster.
5. Run the Ginkgo e2e test suite.
6. Clean up the Kind cluster on exit.

## Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `E2E_IMAGE` | `…:e2e` | IPP image to test |
| `E2E_SIM_IMAGE` | `…sim:v0.9.0` | Model server simulator image |
| `E2E_PORT` | `30080` | Envoy NodePort (host) |
| `E2E_METRICS_PORT` | `30090` | IPP metrics NodePort (host) |
| `E2E_NS` | `ipp-e2e` | Test namespace |
| `KIND_CLUSTER_NAME` | `ipp-e2e` | Kind cluster name |
| `USE_KIND` | `true` | Skip Kind if `false` |
| `SKIP_BUILD` | `false` | Skip image build |
| `K8S_CONTEXT` | _(unset)_ | Use port-forward on external cluster |
| `E2E_PAUSE_ON_EXIT` | _(unset)_ | Pause before cleanup |

## Running Against an Existing Cluster

If you already have a cluster with the payload-processor image
available:

```bash
E2E_IMAGE=ghcr.io/llm-d/llm-d-inference-payload-processor:latest \
USE_KIND=false \
K8S_CONTEXT=my-cluster \
make test-e2e
```

## Test Cases

| Test | What It Validates |
| --- | --- |
| Base model routing | Pool routing via header |
| LoRA adapter routing | ConfigMap adapter lookup |
| Streaming routing | SSE chunks returned |
| Metrics | `ipp_info`, `ipp_success_total` |

## Troubleshooting

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md).
