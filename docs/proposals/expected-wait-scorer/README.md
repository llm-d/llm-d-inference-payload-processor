# Expected-Wait Model Scorer

Author(s): @Mohammad-nassar10


## Summary

The `InflightRequestsScorer` routes to the least-loaded model by request count alone. This ignores model speed and request weight. A model with 100 queued requests at 1000 tokens/sec is a better choice than one with 10 queued requests at 10 tokens/sec — yet the current scorer picks the wrong one.

This proposal introduces an **`ExpectedWaitScorer`** that estimates the total response time for the current request on each candidate model and picks the shortest. It also covers how per-model output throughput is tracked.


---

## Options Considered

### Option A — Request-count × average latency (baseline)

**Formula:** `expected_wait = inflight_requests × avg_latency_ms`

**Data needed:**
- `RequestMetadataCount.Requests` (existing extractor)
- `avg_latency_ms` — running mean of `p.Duration`, tracked per model

**Pros:** Simple. No changes to existing extractors.

**Cons:** Treats a 10-token request and a 10,000-token request as equal queue weight.

---

### Option B — Inflight-tokens ÷ tokens-per-second (token-aware)

**Formula:** `total_time = (inflight_tokens + max_tokens) / tokens_per_sec`

Where:
- `inflight_tokens` = `RequestMetadataCount.Tokens` — sum of `max_tokens` for all in-flight requests, tracked by the existing `RequestMetadataExtractor`
- `max_tokens` — the caller-provided output-length cap for the current request; used as a pessimistic proxy for its processing time (see [Next Steps](#next-steps))
- `tokens_per_sec` = `cumulative_output_tokens / (cumulative_duration_ms / 1000.0)` — measured per model from `usage.completion_tokens` in response bodies

**Pros:** Accounts for both queue depth and the weight of the current request. Correctly weights heavy requests. Aligns with Little's Law.

**Cons:** `max_tokens` is the caller's ceiling, not the expected output length — it overestimates processing time when responses are short. See [Next Steps](#next-steps) for the planned improvement. Requires parsing response bodies for `usage.completion_tokens`; needs `BodyBytes` to be plumbed through the response pipeline.

---

### Option C — Token-aware + current request + EMA (next)

Extends Option B with:

1. **Include the current request** in the wait estimate:
   `expected_wait = (inflight_tokens + current_request_max_tokens) / tokens_per_sec`
2. **EMA instead of cumulative average** (α ≈ 0.1–0.2) — adapts within tens of requests rather than thousands.
3. **Error-rate penalty** — penalise models returning `finish_reason = "error"`.


---
## Implementation

### Throughput Data Source

`tokens_per_sec` requires knowing how many output tokens each model produced and over what duration. Two approaches:

#### Extend `RequestMetadataExtractor`

[PR #124](https://github.com/llm-d/llm-d-inference-payload-processor/pull/124) adds real token-usage tracking to `RequestMetadataExtractor`: on each `ResponseEventType` it already parses `usage.output_tokens` from the response body and accumulates it as `OutputTokens` per model, and has access to `p.Duration`. This is exactly the data needed to compute `tokens_per_sec` — no separate extractor required.

`RequestMetadataCount` gains `TokensPerSec`, computed and written by the existing extractor on every response event:

```go
type RequestMetadataCount struct {
    Requests     int64
    Tokens       int64   // sum of max_tokens for in-flight requests
    OutputTokens int64   // cumulative actual output tokens (PR #124)
    TokensPerSec float64 // recomputed on each response
}
```

`ExpectedWaitScorer` reads a single `"request-metadata"` attribute. No separate `"throughput"` attribute needed.

### New Types

**`pkg/framework/plugins/modelselector/scorer/expectedwait`**

```go
const PluginType = "expected-wait-scorer"
```

`ExpectedWaitScorer.Score` reads the `"request-metadata"` attribute via `datalayer.ReadAttributeKey[T]` and min-max normalises. Models with no throughput data (`tokensPerSec == 0`) receive `expectedWait = 0` — optimistic, so untried models are preferred.

### ResponsePayload — BodyBytes

`pkg/framework/interface/datalayer/datasource.ResponsePayload` gains a new field:

```go
BodyBytes []byte // raw response body snapshot; nil when no body was present
```

`HandleResponseBody` in `pkg/handlers/response.go` passes the raw `responseBodyBytes` (before/after mutation) to the notifier so throughput data (`usage.completion_tokens`) can be parsed without re-serialising the body.


### Next Steps

**Replace `max_tokens` with `avg_completion_tokens`**

`max_tokens` is the caller's output-length ceiling, not the expected output length. A request capped at 4096 tokens may return 50 tokens, leading to an overestimate of processing time.

The correct proxy is the model's observed **average `completion_tokens`** per response, which `RequestMetadataExtractor` can track alongside throughput:

```go
type RequestMetadataCount struct {
    Requests            int64
    Tokens              int64
    OutputTokens        int64
    TokensPerSec        float64
    AvgCompletionTokens float64  // rolling average of usage.completion_tokens
}
```

`ExpectedWaitScorer` would then use `AvgCompletionTokens` instead of `max_tokens`:

```
total_time = (inflight_tokens + avg_completion_tokens) / tokens_per_sec
```

This gives a better-calibrated estimate when responses are typically much shorter than their cap.

**Other future improvements**

- **Time-windowed throughput** — replace the cumulative average with a sliding window (e.g., 1-minute bucketed counters over the last 5 minutes) in `RequestMetadataExtractor`. Only recent observations count toward `TokensPerSec`, so throughput estimates reflect the current load rather than all-time history. The window duration becomes a tunable knob operators can reason about directly.
- **EMA throughput** — alternatively, replace the cumulative average with an exponential moving average (`α ≈ 0.1–0.2`). Simpler state (a single float, no timestamp bookkeeping), but `α` is less intuitive to tune and old data never fully expires.
