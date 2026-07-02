# Configuration

## Table of Contents

- [Overview](#overview)
- [The PayloadProcessorConfig API](#the-payloadprocessorconfig-api)
  - [Top-Level Fields](#top-level-fields)
  - [PluginSpec](#pluginspec)
  - [PluginRef and PluginRefList](#pluginref-and-pluginreflist)
  - [Profiles](#profiles)
  - [Data Layer](#data-layer)
  - [Annotated Example](#annotated-example)
- [Model Mapping ConfigMaps](#model-mapping-configmaps)
  - [Structure](#structure)
  - [Mapping Rules](#mapping-rules)
  - [Multi-Model Example](#multi-model-example)
- [Deployment (Helm)](#deployment-helm)
  - [Representative Values](#representative-values)
  - [Supplying a Custom Config](#supplying-a-custom-config)
- [Command-Line Arguments](#command-line-arguments)
- [Environment Variables](#environment-variables)
- [Proxy Integration](#proxy-integration)
  - [Istio (EnvoyFilter)](#istio-envoyfilter)
  - [GKE (GCPRoutingExtension)](#gke-gcproutingextension)
  - [HTTPRoute Configuration](#httproute-configuration)
  - [Tuning ext-proc Events](#tuning-ext-proc-events)
- [Monitoring](#monitoring)
- [References](#references)

---

## Overview

The Inference Payload Processor (IPP) is configured through three layers:

- **Command-line arguments** — Process-level settings: ports, logging verbosity, tracing, and the path to the config file. See [Command-Line Arguments](#command-line-arguments).
- **A YAML config file** — The `PayloadProcessorConfig`, which declares the plugin pipeline: every plugin instance and how it is composed into profiles and the pre/post stages. This is the heart of IPP's behavior. See [The PayloadProcessorConfig API](#the-payloadprocessorconfig-api).
- **ConfigMaps** — Consumed by certain plugins at runtime. The [`base-model-to-header`] plugin watches labeled ConfigMaps that map model names (base models and LoRA adapters) to base models. See [Model Mapping ConfigMaps](#model-mapping-configmaps).

In a Helm deployment, the config file is rendered into a ConfigMap and mounted into the IPP container; CLI flags are passed through Helm values. See [Deployment (Helm)](#deployment-helm).

---

## The PayloadProcessorConfig API

The `PayloadProcessorConfig` is a YAML document that declares the entire plugin pipeline. The first
two lines are constant and must appear as written:

```yaml
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
```

All plugins are instantiated once under a top-level `plugins` list and then **referenced by name**
from profiles and the pre/post stages. This mirrors the [llm-d Router]'s `EndpointPickerConfig`
model — the same plugin type can be instantiated multiple times under different names.

> [!NOTE]
> IPP does not use a real CRD. The config is read with Kubernetes machinery, but no JSON-Schema
> validation is enforced at admission time; the Kubernetes validation markers in the API types are
> documentation only. Validation happens in the loader at startup.

### Top-Level Fields

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `plugins` | **Yes** | `[]PluginSpec` | The plugin instances to create. Every reference elsewhere resolves to a `name` declared here. |
| `preProcessing` | No | `PluginRefList` | Ordered references intended to run for **every** request before a profile is selected. _Reserved: accepted by the config but not yet invoked by the request path._ |
| `profilePicker` | No | `PluginRef` | The plugin that chooses which profile to run. When exactly one profile is defined and no picker is set, the built-in [`single-profile-picker`] is enabled automatically. |
| `profiles` | **Yes** (min 1) | `[]Profile` | The named profiles. Exactly one runs per request. |
| `postProcessing` | No | `PluginRefList` | Ordered references intended to run for **every** request after the selected profile's response plugins. _Reserved: accepted by the config but not yet invoked by the request path._ |
| `datalayer` | No | `DatalayerConfig` | Data-layer plugin references: `collectors`, `extractors`, and `datasources`. |

### PluginSpec

Each entry in the top-level `plugins` list declares one plugin instance:

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `name` | No | string | Name by which other entries reference this instance. Defaults to the value of `type` when omitted. |
| `type` | **Yes** | string | The plugin type to instantiate (e.g. `body-field-to-header`). See [Plugins] for available types. |
| `parameters` | No | raw JSON/YAML | Opaque parameters passed to the plugin's factory function, which is responsible for parsing them. The schema varies per plugin. |

### PluginRef and PluginRefList

A `PluginRef` points at an instance declared in `plugins`:

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `pluginRef` | **Yes** | string | The `name` of a plugin instance in the top-level `plugins` list. |
| `weight` | For scorers | float | Weight applied to a Scorer's contribution. **Required** when the referenced plugin is a Scorer (the loader rejects a scorer reference with no weight); ignored for non-scorer references. |

A `PluginRefList` (used by `preProcessing` and `postProcessing`) is simply an object with a `plugins`
list of `PluginRef` entries.

### Profiles

A **profile** is a named set of request and response plugin references. Exactly one profile runs per
request, chosen by the profile picker.

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `name` | **Yes** | string | The profile's name. |
| `plugins` | **Yes** | object | Holds two ordered lists: `request` (`[]PluginRef`) and `response` (`[]PluginRef`). |

A `request` entry may reference **either** a request-processor plugin **or** a model-selector plugin
(Filter / Scorer / Picker); the config loader routes each reference to the correct extension point
based on the interface the referenced plugin implements. Scorer references may carry a `weight`. See
the [Architecture] doc for how the `Filter → Score → Pick` pipeline composes.

### Data Layer

The optional `datalayer` section registers plugins that maintain cross-request state consumed by
Filters and Scorers. It holds three `PluginRef` lists:

| Field | Type | Description |
|-------|------|-------------|
| `collectors` | `[]PluginRef` | Collector plugins that aggregate signals over time. |
| `extractors` | `[]PluginRef` | Extractor plugins that pull metadata out of request/response events. |
| `datasources` | `[]PluginRef` | DataSource plugins that import external configuration into the store. |

### Annotated Example

A complete config that performs multi-pool routing (model-name extraction plus LoRA-to-base mapping)
under a single auto-selected profile:

```yaml
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig

# Instantiate every plugin once. Each instance is addressable by `name`
# (which defaults to `type` when omitted).
plugins:
- type: body-field-to-header           # copy `model` from the body into a header
  parameters:
    fieldName: model
    headerName: X-Gateway-Model-Name
- type: base-model-to-header           # map model/adapter name to its base model
                                       # and inject X-Gateway-Base-Model-Name

# Optional: runs for every request before profile selection.
# preProcessing:
#   plugins:
#   - pluginRef: some-preprocessor

# Optional: when a single profile is defined, `single-profile-picker`
# is enabled automatically, so this can be omitted.
# profilePicker:
#   pluginRef: single-profile-picker

# At least one profile is required; exactly one runs per request.
profiles:
- name: default
  plugins:
    request:                           # ordered request-side pipeline
    - pluginRef: body-field-to-header
    - pluginRef: base-model-to-header
    response: []                       # no response-side processing

# Optional: runs for every request after the profile's response plugins.
# postProcessing:
#   plugins:
#   - pluginRef: some-postprocessor

# Optional: cross-request state for Filters/Scorers.
# datalayer:
#   collectors: []
#   extractors: []
#   datasources: []
```

A model-selection profile mixes request processors with model-selector plugins in the same `request`
list and weights a scorer:

```yaml
profiles:
- name: model-selection
  plugins:
    request:
    - pluginRef: model-selector            # entry point for Filter → Score → Pick
    - pluginRef: inflight-requests-scorer  # a Scorer
      weight: 1.0
    - pluginRef: max-score-picker          # the Picker
    response: []
```

---

## Model Mapping ConfigMaps

Multi-pool routing requires IPP to know which base model each requested model name corresponds to.
This mapping is supplied by Kubernetes ConfigMaps that the [`base-model-to-header`] plugin watches and
loads at runtime — no IPP restart is needed when they change.

### Structure

Each ConfigMap defines **one** base model and its LoRA adapters. To be watched by IPP, a ConfigMap
must carry the label `inference.llm-d.ai/ipp-managed: "true"`.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: qwen-model-mapping
  labels:
    inference.llm-d.ai/ipp-managed: "true"
data:
  baseModel: Qwen/Qwen2.5-1.5B-Instruct
  adapters: |
    - qwen-summarizer
    - qwen-classifier
```

| Data field | Required | Type | Description |
|------------|----------|------|-------------|
| `baseModel` | **Yes** | string | The base model name. Requests naming this model map to itself. |
| `adapters` | No | YAML list | LoRA adapter names served by this base model. Each adapter name maps back to `baseModel`. |

### Mapping Rules

- A requested model name that matches a `baseModel` maps to that base model.
- A requested model name that matches an entry in an `adapters` list maps to that ConfigMap's `baseModel`.
- The resolved base model is injected as the `X-Gateway-Base-Model-Name` header, which `HTTPRoute` rules match on to select the [InferencePool]. See [HTTPRoute Configuration](#httproute-configuration).
- A name that matches nothing yields an empty base-model header; configure your `HTTPRoute` rules accordingly.

> [!IMPORTANT]
> Model names — both base models and adapters — must be **globally unique across all pools**. Because
> IPP resolves a name to exactly one base model, a name appearing in more than one ConfigMap (or
> reused as both a base model and an adapter) makes routing ambiguous. Keep one ConfigMap per base
> model and ensure no name collisions across them.

### Multi-Model Example

Two base models, each in its own ConfigMap, with their adapters:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: qwen-model-mapping
  labels:
    inference.llm-d.ai/ipp-managed: "true"
data:
  baseModel: Qwen/Qwen2.5-1.5B-Instruct
  adapters: |
    - qwen-summarizer
    - qwen-classifier
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: deepseek-model-mapping
  labels:
    inference.llm-d.ai/ipp-managed: "true"
data:
  baseModel: deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B
  # adapters is optional; this base model serves no LoRA adapters.
```

With this mapping, a request for `qwen-summarizer` resolves to `Qwen/Qwen2.5-1.5B-Instruct`, while a
request for `deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B` resolves to itself — each routed to its own
pool by the `HTTPRoute` rules below.

> [!NOTE]
> By default IPP watches only the namespace it is deployed in (the `NAMESPACE` env var). To watch
> ConfigMaps across namespaces, set the Helm value `payloadProcessor.multiNamespace=true`, which
> omits the `NAMESPACE` env var. See [Environment Variables](#environment-variables).

---

## Deployment (Helm)

IPP ships a Helm chart that provisions the Deployment, Service, RBAC, the config ConfigMap, and the
provider-specific proxy integration. The chart is deployed **once per Gateway**.

```bash
helm install payload-processor ./config/charts/payload-processor \
    --set provider.name=istio \
    --set inferenceGateway.name=inference-gateway
```

The full values table is documented in the chart README — see the [Helm Chart] reference. This section
only highlights the values most relevant to configuration; it does not re-document every value.

### Representative Values

```yaml
payloadProcessor:
  name: payload-processor
  replicas: 1
  port: 9004              # ext_proc gRPC port
  healthCheckPort: 9005   # gRPC health/readiness port
  multiNamespace: false   # true → watch ConfigMaps across namespaces
  image:
    registry: ghcr.io/llm-d
    repository: llm-d-inference-payload-processor
    tag: main
    pullPolicy: IfNotPresent

  # CLI flags passed through to the binary as --<key>=<value>.
  flags:
    v: 3                  # log verbosity

  # Tracing (OTEL). When enabled, the chart injects OTEL_* env vars.
  tracing:
    enabled: false
    otelServiceName: "inference.llm-d.ai/inference-payload-processor"
    otelExporterEndpoint: "http://localhost:4317"
    sampling:
      sampler: "parentbased_traceidratio"
      samplerArg: "0.1"

provider:
  name: none              # istio | gke | none
  supportedEvents:
    requestHeaders: true
    requestBody: true
    requestTrailers: true
    responseHeaders: true
    responseBody: true
    responseTrailers: true

inferenceGateway:
  name: inference-gateway
```

### Supplying a Custom Config

By default the chart mounts a built-in `PayloadProcessorConfig` (the shipped default lives in
[`deploy/config/ipp-config.yaml`](../deploy/config/ipp-config.yaml)). To supply your own pipeline, set
`payloadProcessor.customConfig` to a `PayloadProcessorConfig` body; the chart renders it into the
mounted ConfigMap and points `--config-file` at it.

```yaml
payloadProcessor:
  customConfig:
    # The chart adds the apiVersion/kind header automatically — start at `plugins`.
    plugins:
    - type: body-field-to-header
      parameters:
        fieldName: model
        headerName: X-Gateway-Model-Name
    - type: base-model-to-header
    profiles:
    - name: default
      plugins:
        request:
        - pluginRef: body-field-to-header
        - pluginRef: base-model-to-header
```

> [!NOTE]
> The custom config is the same `PayloadProcessorConfig` schema documented in
> [The PayloadProcessorConfig API](#the-payloadprocessorconfig-api). Model-mapping ConfigMaps are
> applied separately from the chart, not embedded in `customConfig`.

---

## Command-Line Arguments

IPP reads its process configuration from these flags (defined in `pkg/server/options.go` and the
logging options). In Helm, set them via `payloadProcessor.flags` (which renders each as
`--<key>=<value>`); `--config-file`, `NAMESPACE`, and the OTEL flags are wired by the chart.

| Flag | Default | Description |
|------|---------|-------------|
| `--config-file` | _(empty)_ | Path to the `PayloadProcessorConfig` YAML file. |
| `--config-text` | _(empty)_ | The `PayloadProcessorConfig` provided inline as text, in lieu of a file. |
| `--grpc-port` | `9004` | gRPC port used for ext_proc communication with the proxy. |
| `--grpc-health-port` | `9005` | Port for gRPC liveness and readiness probes. |
| `--metrics-port` | `9090` | Port exposing the Prometheus `/metrics` endpoint. |
| `--metrics-endpoint-auth` | `true` | Enable authentication and authorization on the metrics endpoint. |
| `--secure-serving` | `true` | Serve the ext-proc gRPC endpoint over TLS (a self-signed certificate is generated at startup). Set to `false` for plaintext. |
| `--tracing` | `true` | Enable emitting OpenTelemetry traces. |
| `--enable-pprof` | `true` | Enable pprof handlers. Set to `false` to disable. |
| `-v`, `--v` | `2` | Log verbosity level. |
| `--zap-log-level` | _(derived from `-v`)_ | Zap log level. When unset, it is derived as `-1 × v`. |

The logger also accepts the standard controller-runtime `--zap-*` flags (`--zap-devel`,
`--zap-encoder`, `--zap-stacktrace-level`, `--zap-time-encoding`) for additional tuning.

> [!NOTE]
> `--config-file` and `--config-text` are two ways to supply the config; if both are set,
> `--config-text` takes precedence. The three server ports (`--grpc-port`, `--grpc-health-port`,
> `--metrics-port`) are validated to be in `1–65535` and must all differ.

---

## Environment Variables

| Variable | Set by | Description |
|----------|--------|-------------|
| `NAMESPACE` | Helm (unless `multiNamespace=true`) | Restricts the controller cache — and therefore ConfigMap watching — to this single namespace. When unset, IPP watches all namespaces. |
| `OTEL_SERVICE_NAME` | Helm (tracing) | Service name reported on traces. Defaults to `llm-d-ipp` if unset. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Helm (tracing) | OTLP collector endpoint. Defaults to `http://localhost:4317` if unset. |
| `OTEL_TRACES_EXPORTER` | Helm (tracing) | Traces exporter (the chart sets `otlp`). |
| `OTEL_TRACES_SAMPLER` | Helm (tracing) | Sampler type (e.g. `parentbased_traceidratio`). |
| `OTEL_TRACES_SAMPLER_ARG` | Helm (tracing) | Sampler argument (e.g. `0.1`). |
| `OTEL_RESOURCE_ATTRIBUTES` | Helm (tracing) | Resource attributes attached to traces (namespace, node, pod). |

> [!NOTE]
> The `OTEL_*` variables only take effect when tracing is enabled (`--tracing=true`, set via
> `payloadProcessor.tracing.enabled` in Helm). The chart also populates `OTEL_RESOURCE_ATTRIBUTES`
> from the pod's namespace, node, and pod name via the downward API.

---

## Proxy Integration

IPP runs as an ext-proc service that the proxy invokes over Envoy's [External Processing (ext-proc)]
protocol. The Helm chart provisions the provider-specific integration based on `provider.name`:
`istio` generates an `EnvoyFilter`, `gke` generates a `GCPRoutingExtension`, and `none` provisions the
core IPP resources (Deployment, Service, config, RBAC) but no proxy-integration resources — you wire
the proxy integration yourself. In all cases the request
body is streamed using ext-proc's `FULL_DUPLEX_STREAMED` body mode, which IPP requires to observe and
mutate full bodies.

### Istio (EnvoyFilter)

With `provider.name=istio`, the chart installs an `EnvoyFilter` that inserts the ext_proc filter into
the target Gateway's HTTP filter chain (and a `DestinationRule` for the IPP service). The processing
mode for each event reflects the `provider.supportedEvents` values — enabled events use `SEND`/
`FULL_DUPLEX_STREAMED`, disabled ones use `SKIP`/`NONE`:

```yaml
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: payload-processor
spec:
  targetRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: inference-gateway
  configPatches:
  - applyTo: HTTP_FILTER
    match:
      context: GATEWAY
      listener:
        filterChain:
          filter:
            name: "envoy.filters.network.http_connection_manager"
    patch:
      operation: INSERT_FIRST
      value:
        name: envoy.filters.http.ext_proc.payload-processor
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
          failure_mode_allow: false
          allow_mode_override: true
          processing_mode:
            request_header_mode: "SEND"
            response_header_mode: "SEND"
            request_body_mode: "FULL_DUPLEX_STREAMED"
            response_body_mode: "FULL_DUPLEX_STREAMED"
            request_trailer_mode: "SEND"
            response_trailer_mode: "SEND"
          grpc_service:
            envoy_grpc:
              cluster_name: outbound|9004||payload-processor.<namespace>.svc.cluster.local
```

The filter insertion point is controlled by `provider.istio.envoyFilter.operation` (default
`INSERT_FIRST`) and `provider.istio.envoyFilter.anchorSubFilter`.

### GKE (GCPRoutingExtension)

With `provider.name=gke`, the chart registers IPP as a routing extension via a `GCPRoutingExtension`
(and a `HealthCheckPolicy`). The `supportedEvents` list and body send modes again follow
`provider.supportedEvents`:

```yaml
kind: GCPRoutingExtension
apiVersion: networking.gke.io/v1
metadata:
  name: payload-processor
spec:
  targetRefs:
  - group: "gateway.networking.k8s.io"
    kind: Gateway
    name: inference-gateway
  extensionChains:
  - name: chain1
    extensions:
    - name: ext1
      authority: "myext.com"
      timeout: 1s
      supportedEvents:
      - RequestHeaders
      - RequestBody
      - RequestTrailers
      - ResponseHeaders
      - ResponseBody
      - ResponseTrailers
      requestBodySendMode: "FullDuplexStreamed"
      responseBodySendMode: "FullDuplexStreamed"
      backendRef:
        group: ""
        kind: Service
        name: payload-processor
        port: 9004
```

### HTTPRoute Configuration

IPP injects a routing header (the [`base-model-to-header`] plugin uses `X-Gateway-Base-Model-Name`);
**you** then configure `HTTPRoute` resources that match on that header and route to the right
[InferencePool]. These `HTTPRoute` resources are **not** created by the chart — they are part of your
deployment.

A two-pool example routing on the injected base-model header:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: qwen-route
spec:
  parentRefs:
  - name: inference-gateway
  rules:
  - matches:
    - headers:
      - type: Exact
        name: X-Gateway-Base-Model-Name
        value: Qwen/Qwen2.5-1.5B-Instruct
    backendRefs:
    - group: inference.networking.k8s.io
      kind: InferencePool
      name: qwen-pool
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: deepseek-route
spec:
  parentRefs:
  - name: inference-gateway
  rules:
  - matches:
    - headers:
      - type: Exact
        name: X-Gateway-Base-Model-Name
        value: deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B
    backendRefs:
    - group: inference.networking.k8s.io
      kind: InferencePool
      name: deepseek-pool
```

A request for the LoRA adapter `qwen-summarizer` is mapped to `Qwen/Qwen2.5-1.5B-Instruct` by IPP and
matched by the first route; a request for the DeepSeek base model is matched by the second.

### Tuning ext-proc Events

The six ext-proc events — `requestHeaders`, `requestBody`, `requestTrailers`, `responseHeaders`,
`responseBody`, `responseTrailers` — are individually toggleable through `provider.supportedEvents`.
**Each enabled event is an extra network hop** between the proxy and IPP, so enable only the events
your configured plugins actually consume. For example, a routing-only deployment (which acts on the
request body alone) can disable all response events:

```yaml
provider:
  name: istio
  supportedEvents:
    requestHeaders: true
    requestBody: true
    requestTrailers: true
    responseHeaders: false
    responseBody: false
    responseTrailers: false
```

---

## Monitoring

IPP exposes Prometheus metrics on an HTTP endpoint (default port `9090`, path `/metrics`), configured
via `--metrics-port` and `--metrics-endpoint-auth`. The full list of metrics — their names, types,
labels, and intended use — is documented in [Metrics]. Tracing is handled separately via OpenTelemetry;
see [Environment Variables](#environment-variables).

---

## References

- [Architecture] — How IPP works: ext-proc integration, the processing pipeline, profiles, model selection, and multi-pool routing.
- [Plugins] — In-tree plugin reference and the pipeline configuration model.
- [Metrics] — Prometheus metrics exposed by IPP.
- [Helm Chart] — Chart install reference and the full values table.
- [llm-d] — The end-to-end **Multi-Model Routing guide** lives in the llm-d repo.
- [External Processing (ext-proc)] — The Envoy protocol IPP implements.

[Architecture]: architecture.md
[Plugins]: plugins.md
[Metrics]: metrics.md
[Helm Chart]: ../config/charts/payload-processor/README.md
[`base-model-to-header`]: plugins.md
[`single-profile-picker`]: plugins.md
[llm-d]: https://github.com/llm-d/llm-d
[llm-d Router]: https://github.com/llm-d/llm-d-router
[InferencePool]: https://gateway-api-inference-extension.sigs.k8s.io
[External Processing (ext-proc)]: https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/ext_proc_filter
