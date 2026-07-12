# router-metrics-collector

Polls each model's pool-aggregated Prometheus metrics endpoint and writes a per-model `RouterMetrics` attribute carrying KV-cache utilization, CPU-cache usage, queue depth, and running-request count.


## Plugin configuration

```yaml
plugins:
  - type: router-metrics-collector
    parameters:
      interval: "30s"
      timeout:  "5s"
      utilizationMetric:     "vllm:kv_cache_usage_perc"
      queueDepthMetric:      "vllm:num_requests_waiting"
      cpuCacheUsageMetric:   "vllm:cpu_cache_usage_perc"
      runningRequestsMetric: "vllm:num_requests_running"
      maxConcurrent: 8

datalayer:
  collectors:
    - pluginRef: router-metrics-collector
```

## Per-model input

The collector reads each model's `metrics-endpoint` attribute, attached by
`modelconfigcollector` when a model entry in `models.json` has a non-empty
`metricsURL`:

```json
{ "models": [
    { "name": "llama-3.1-8b",
      "metricsURL": "http://llama-pool.svc:9090/federate?match[]=..." } ] }
```

The URL is contractually a *pool-aggregated* endpoint (Prometheus `/federate`, a thin aggregator, or — for dev — a single representative pod). The collector does no pod discovery and no client-side aggregation.

## What it writes (per scraped model)

| Where | Contents |
|--|--|
| Datastore attribute `router-metrics` | `datalayer.RouterMetrics{KVCacheUtilization, CPUCacheUtilization, WaitingRequests, RunningRequests, LastObservedAt}` |
| Prometheus gauge `ipp_kv_cache_utilization_ratio{model}` | Mirror of the last observed KV-cache utilization (0–1) |
| Prometheus gauge `ipp_kv_cache_cpu_usage_ratio{model}` | Mirror of the last observed CPU-cache usage (0–1) |
| Prometheus gauge `ipp_kv_cache_queue_depth{model}` | Mirror of the last observed sum of waiting requests |
| Prometheus gauge `ipp_kv_cache_running_requests{model}` | Mirror of the last observed sum of running requests |
| Prometheus histogram `ipp_kv_cache_scrape_duration_seconds{model}` | Per-scrape latency (successes and failures) |
| Prometheus counter `ipp_kv_cache_scrape_failures_total{model, reason}` | Failure counts by reason (`dial`, `timeout`, `http_status`, `parse`, `request_build`) |

On failure the prior attribute and gauge are left in place; freshness is gated
on `LastObservedAt` by downstream consumers.
