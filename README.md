[![CI](https://github.com/llm-d/llm-d-inference-payload-processor/actions/workflows/ci-pr-checks.yaml/badge.svg)](https://github.com/llm-d/llm-d-inference-payload-processor/actions/workflows/ci-pr-checks.yaml)
[![Go Reference](https://pkg.go.dev/badge/github.com/llm-d/llm-d-inference-payload-processor.svg)](https://pkg.go.dev/github.com/llm-d/llm-d-inference-payload-processor)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Join Slack](https://img.shields.io/badge/Join_Slack-blue?logo=slack)](https://llm-d.slack.com/archives/C08SBNRRSBD)
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fllm-d%2Fllm-d-inference-payload-processor.svg?type=shield)](https://app.fossa.com/projects/git%2Bgithub.com%2Fllm-d%2Fllm-d-inference-payload-processor?ref=badge_shield)

# llm-d Inference Payload Processor

The **Inference Payload Processor (IPP)** is a pluggable framework for inspecting and mutating
inference request and response payloads in the llm-d data plane. It runs as an
[External Processing (ext-proc)] service alongside the inference gateway's Proxy, which streams each
request and response to IPP for real-time, payload-aware processing.

Because IPP sees the full payload, it can shape requests and responses in arbitrary ways — any logic
that benefits from reading or rewriting the body, headers, or trailers can be expressed as a plugin.
Its flagship use is **payload-aware routing**: extracting signals from the request (such as the model
name) and injecting headers so a single Gateway endpoint can front many models and LoRA adapters. This
composes with the [llm-d Router]'s [Endpoint Picker (EPP)] — IPP can decide **which pool** serves a
request while the EPP decides **which pod** within that pool — but routing is one application of a
general framework, not its limit.

<p align="center">
  <img src="docs/images/ipp-request-flow.svg" width="800" alt="IPP Request Flow">
</p>

## Core Capabilities

- **Request processing** — Inspect and mutate request headers, body, or trailers before routing.
- **Response processing** — Inspect and mutate response headers, body, or trailers on the way back to the client.
- **Payload-aware routing** — Extract signals from the request body (e.g. the model name) and inject routing headers so the Proxy can select the correct destination (e.g., [InferencePool]). This powers **multi-pool routing**: serving multiple base models and LoRA adapters behind one OpenAI-compatible endpoint.
- **Model selection** — A pluggable `Filter → Score → Pick` pipeline that chooses *which* model serves a request (e.g. for cost or load-aware routing), adapting the upstream [Scheduler Architecture] pattern at the model level. See the [ModelSelector proposal].
- **Extensibility** — All behavior is implemented as plugins configured via a YAML `PayloadProcessorConfig`. Add your own without forking the framework — see [Creating a Plugin].

## Modes of Operation

IPP is deployed once per Gateway as a standalone service and wired into the Proxy via ext-proc. The
Helm chart provisions the provider-specific integration automatically:

- **Istio** — Installs an `EnvoyFilter` that inserts the ext-proc filter into the Gateway's filter chain.
- **GKE** — Installs a `GCPRoutingExtension` that registers IPP as a routing extension.
- **None** — Deploys the core IPP resources (Deployment, Service, config, RBAC) but no proxy integration; you wire that up yourself.

## Documentation

| Document | Description |
|----------|-------------|
| [Architecture](docs/architecture.md) | How IPP works: ext-proc integration, the processing pipeline, profiles, model selection, and multi-pool routing. |
| [Configuration](docs/configuration.md) | Full configuration reference: the `PayloadProcessorConfig` API, Helm values, env vars, CLI flags, ConfigMaps, and proxy integration. |
| [Plugins](docs/plugins.md) | Reference for all in-tree plugins and how the pipeline composes them. |
| [Creating a Plugin](docs/create_new_plugin.md) | Tutorial for writing and registering a custom plugin. |
| [Metrics](docs/metrics.md) | Prometheus metrics exposed by IPP. |
| [Helm Chart](config/charts/payload-processor/README.md) | Chart install reference and values table. |
| [ModelSelector Proposal](docs/proposals/043-model-selection-framework/README.md) | Design of the model-selection framework. |

For end-to-end deployment, see the [llm-d] project documentation and guides.

## Terminology

- **IPP (Inference Payload Processor)** — This service. Inspects and mutates request/response payloads via ext-proc; among other things, it can contribute pool-level routing signals.
- **Plugin** — A user-configurable unit of behavior (request processor, response processor, model-selector Filter/Scorer/Picker, profile picker, or data-layer collector/extractor/datasource). Plugins are selected and ordered in the `PayloadProcessorConfig`.
- **Profile** — A named set of request and response plugins. A request executes exactly one profile, chosen by the profile picker.
- **ModelSelector** — The `Filter → Score → Pick` framework that selects a *model* (not an endpoint) for a request.
- **Proxy** — The L7 proxy (e.g. Envoy) that invokes IPP over ext-proc.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Active docs work lands on the
`docs` branch first; branch from it and open PRs against it so the documentation can be reviewed and
assembled collaboratively before merging to `main`.

[llm-d]: https://github.com/llm-d/llm-d
[llm-d Router]: https://github.com/llm-d/llm-d-router
[Endpoint Picker (EPP)]: https://github.com/llm-d/llm-d-router
[InferencePool]: https://gateway-api-inference-extension.sigs.k8s.io
[External Processing (ext-proc)]: https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/ext_proc_filter
[Scheduler Architecture]: https://github.com/kubernetes-sigs/gateway-api-inference-extension/tree/main/docs/proposals/0845-scheduler-architecture-proposal
[ModelSelector proposal]: docs/proposals/043-model-selection-framework/README.md
[Creating a Plugin]: docs/create_new_plugin.md


## License
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fllm-d%2Fllm-d-inference-payload-processor.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2Fllm-d%2Fllm-d-inference-payload-processor?ref=badge_large)