# Metrics

The **Inference Payload Processor (IPP)** exposes [Prometheus] metrics for build information, request
processing, per-plugin latency, and model selection. They are served on a dedicated endpoint (default
port `9090`, path `/metrics`).

All metric names carry the `ipp_` prefix (the `ipp` Prometheus subsystem). Every metric is published
at the **ALPHA** stability level, reflected as an `[ALPHA]` annotation at the start of each metric's
help text.

## Metrics Reference

| Name | Type | Labels | Description |
|------|------|--------|-------------|
| `ipp_info` | Gauge | `commit`, `build_ref` | Build information of the running IPP. |
| `ipp_success_total` | Counter | _(none)_ | Requests processed successfully. |
| `ipp_body_field_not_found_total` | Counter | `field` | Times a `field` was not found in a request body. |
| `ipp_body_field_empty_total` | Counter | `field` | Times a `field` was found in a request body but was empty. |
| `ipp_plugin_duration_seconds` | Histogram | `extension_point`, `plugin_type`, `plugin_name` | Per-plugin processing latency, by [pipeline][processing pipeline] extension point. |
| `ipp_model_selector_e2e_duration_seconds` | Histogram | _(none)_ | End-to-end [model selection][ModelSelector] latency. |
| `ipp_model_selector_attempt_total` | Counter | `status` | Model-selection attempts, by `status` (`success` / `failure`). |
| `ipp_request_ttft_seconds` | Histogram | `model` | Time to first token, per `model`. |

Notes:

- `ipp_info` is emitted once at startup with a constant value of `1`; the build identity lives in its labels. Join on it to attribute other series to a build.
- `ipp_body_field_empty_total` distinguishes a present-but-empty field from a missing one (`ipp_body_field_not_found_total`).
- `status="failure"` on `ipp_model_selector_attempt_total` covers cases such as filtering leaving zero candidate models.
- Histogram buckets: `ipp_plugin_duration_seconds` and `ipp_model_selector_e2e_duration_seconds` span `0.0001s`–`0.1s` (sub-second plugin work); `ipp_request_ttft_seconds` spans `0.05s`–`30s` (end-to-end serving latency).

## Scraping

Metrics are served by the controller-runtime metrics server on `--metrics-port` (default `9090`) at
`/metrics`. Access is governed by `--metrics-endpoint-auth`, **enabled by default**: when on, scrapers
must present Kubernetes credentials authorized to read the endpoint; set it to `false` to serve
without authentication. See [Configuration] for how these flags are wired through the Helm chart.

## References

- [Configuration] — Metrics serving and CLI flags.
- [Architecture] — ext-proc integration, the processing pipeline, and model selection.
- [metrics.go] — Authoritative metric definitions in source.

[Configuration]: configuration.md
[Architecture]: architecture.md
[processing pipeline]: architecture.md#processing-pipeline
[ModelSelector]: architecture.md#model-selection
[metrics.go]: https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/metrics/metrics.go
[Prometheus]: https://prometheus.io
