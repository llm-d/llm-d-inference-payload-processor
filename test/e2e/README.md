# E2E Tests

End-to-end tests for the `llm-d-inference-payload-processor`.

These tests deploy a complete stack on a
[Kind](https://kind.sigs.k8s.io/) cluster and validate the payload
processor's behaviour through an Envoy proxy against model-server
simulators.

## Architecture

```text
curl pod ──► Envoy (ext_proc) ──► Payload Processor
                │                        │
                │  route by header       │ ConfigMap watch
                ▼                        ▼
       Llama / DeepSeek             K8s API
       model-server sims
```

The payload processor extracts the `model` field from the request
body, resolves adapter names to base models via ConfigMaps, and sets
the `X-Gateway-Base-Model-Name` header. Envoy routes the request to
the correct model-server cluster based on that header.

## Prerequisites

- Go (version in `go.mod`)
- Docker
- [Kind](https://kind.sigs.k8s.io/)
  (`go install sigs.k8s.io/kind@latest`)

## Quick Start

The simplest way to run the e2e tests from the repo root:

```bash
make test-e2e
```

This will:

1. Build the payload-processor container image for the local
   architecture.
2. Create a Kind cluster named `pp-e2e` (if one doesn't exist).
3. Load the image into the cluster.
4. Run the Ginkgo e2e test suite.

## Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `E2E_IMAGE` | `…:e2e` | PP image to test |
| `MANIFEST_PATH` | (see below) | DeepSeek manifest path |
| `E2E_NS` | `pp-e2e` | Test namespace |
| `KIND_CLUSTER_NAME` | `pp-e2e` | Kind cluster name |
| `USE_KIND` | `true` | Skip Kind if `false` |
| `SKIP_BUILD` | `false` | Skip image build |
| `E2E_PAUSE_ON_EXIT` | _(unset)_ | Pause before cleanup |

`MANIFEST_PATH` defaults to
`test/testdata/deepseek-model-server.yaml`.

## Running Against an Existing Cluster

If you already have a cluster with the payload-processor image
available:

```bash
E2E_IMAGE=ghcr.io/llm-d/llm-d-inference-payload-processor:latest \
USE_KIND=false \
make test-e2e
```

## Test Cases

| Test | What It Validates |
| --- | --- |
| Base model routing | Pool routing via header |
| LoRA adapter routing | ConfigMap adapter lookup |
| Streaming routing | SSE chunks returned |
| Metrics | `bbr_info`, `bbr_success_total` |

## Troubleshooting

### Tests fail during setup (pods not ready)

Check pod status in the e2e namespace:

```bash
kubectl get pods -n pp-e2e
kubectl describe pod -n pp-e2e <pod-name>
```

### Manually inspect the cluster after a run

Set `E2E_PAUSE_ON_EXIT=30m` to keep the cluster alive after
tests complete:

```bash
E2E_PAUSE_ON_EXIT=30m make test-e2e
```

### Clean up the Kind cluster

```bash
kind delete cluster --name pp-e2e
```
