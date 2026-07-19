# TTFT-Aware Scorer

Routes each request to the model with the lowest predicted TTFT under current load.

## Equations

Every TTFT decomposes as `TTFT = prefill_time + queue_wait`.

**P10Low** — hardware-bound service floor (published by the extractor):

The extractor keeps a bounded history of per-bucket P10s: once per `bucketDuration` it records
the P10 TTFT of that bucket, and `P10Low` is the P10 of that history. When the history is full
(`bucketHistorySize` entries) the smallest and largest entries are evicted — not the oldest — so
a single anomalously fast or slow bucket never sticks.

This is load-invariant and robust: the history spans idle and busy buckets, and taking a low
percentile of it locks onto the idle buckets (the true prefill floor) instead of drifting up with
recent load. Prefill time does not change with queue depth or concurrency, so the floor is stable.

**Operating points** — measured over a short window (default 3m / 100 requests, kept responsive):
```
P25, P50            = 25th / 50th percentile TTFT
inflightAtP25       = average inflight_at_dispatch in the P15-P35 band
inflightAtP50       = average inflight_at_dispatch in the P40-P60 band
```
Averaging inflight over a band rather than a single observation stabilises the estimate.

**effectiveTTFT** — predicted TTFT for a request arriving now.

A line through the high operating point `(inflightAtP50, P50)` and a low anchor that blends between
the in-cloud point `(inflightAtP25, P25)` and the load-free floor `(0, P10Low)`:
```
w             = clamp((inflightAtP50 - inflightAtP25) / anchorGapScale, 0, 1)
lowInflight   = w * inflightAtP25
lowTTFT       = w * P25 + (1 - w) * P10Low
effectiveTTFT = lowTTFT + (inflight - lowInflight) * (P50 - lowTTFT) / (inflightAtP50 - lowInflight)
```
Clamped to `>= P10Low`. Under-observed models (not yet calibrated) seed at `P10Low`.

**Score:**
```
score = (maxTTFT - effectiveTTFT) / (maxTTFT - minTTFT)
```
Under-observed models (UNOBSERVED or SEED state) receive an optimistic high score. With
`explorationRate > 0`, that high score is suppressed to 0 with probability `(1 - explorationRate)`
so only ~`explorationRate` of requests probe the under-observed model for calibration.

## Why it works physically

When more requests are in flight, a new request waits longer in the queue, so TTFT rises with
inflight — and it rises *faster than linearly* as the server approaches saturation (queueing).

The scorer draws a line through two points it has actually observed:

- fast requests ran at lower load: `(inflightAtP25, P25)`
- median requests at higher load: `(inflightAtP50, P50)`

Because both anchors sit *inside* the observed load cloud, the line follows the **local slope** of
that convex curve — so it does not systematically under-predict the way a single chord drawn from a
synthetic zero-load floor does.

At low load the two anchors collapse together (every request sees similar, low inflight), so their
slope is ill-defined. The blend weight `w` handles this smoothly: as the inflight gap shrinks, `w →
0` slides the low anchor down to `(0, P10Low)`, recovering the stable floor chord. There is no
threshold and no discontinuity — the prediction transitions continuously between the two regimes,
and the denominator `inflightAtP50 - w*inflightAtP25` can never collapse. The only knob,
`anchorGapScale`, is a numerical-conditioning scale (how much inflight separation counts as "well
separated"), not a fitted parameter.

## Parameters

### Scorer (`ttft-aware-scorer`)

| Parameter | Default | Description |
|---|---|---|
| `explorationRate` | 0.0 | Fraction of requests routed to under-observed models for calibration probing. 0 = all traffic to the trusted winner; 0.1 = ~10% probe. |
| `anchorGapScale` | 2.0 | Inflight separation (`inflightAtP50 - inflightAtP25`) at which the prediction fully trusts the in-cloud secant; below it the low anchor blends toward the floor chord. Must be > 0. |

### Extractor (`ttft-percentile-extractor`)

| Parameter | Default | Description |
|---|---|---|
| `maxObservationAge` | 3m | Time bound for the short window (P25 / P50 / P10) |
| `maxRequests` | 100 | Cap the short window to the most recent N observations |
| `minRequests` | 10 | Minimum capped-window count before the scorer trusts the operating point |
| `windowSize` | 5000 | Ring buffer capacity (~200 KB per model) |
| `bucketDuration` | 1m | Window for each floor-history entry's P10; keep `<= maxObservationAge` |
| `bucketHistorySize` | 720 | Per-bucket P10s kept for the floor (`bucketDuration * bucketHistorySize` = horizon, 12h); min/max evicted when full |

## Example configuration

An end-to-end Helm values override wiring the scorer together with the TTFT extractor,
the model-config datasource, and a picker:

```yaml
payloadProcessor:
  customConfig:
    plugins:
    - type: body-field-to-header
      parameters:
        fieldName: model
        headerName: X-Gateway-Model-Name
    - type: base-model-to-header
    - type: model-selector
    - type: ttft-aware-scorer
      parameters:
        explorationRate: 0.1          # 10% of requests probe under-observed models; 0 = disabled
        anchorGapScale: 2.0           # inflight separation at which the in-cloud secant is fully trusted
    - type: max-score-picker
    - type: ttft-percentile-extractor
      parameters:
        intervalDuration: 1s
        windowSize: 5000
        maxObservationAge: 3m
        maxRequests: 100
        minRequests: 20
        bucketDuration: 1m
        bucketHistorySize: 720
    - type: model-config-datasource
      parameters:
        modelsPath: /config/models.json
    profiles:
    - name: default
      plugins:
        request:
        - pluginRef: model-selector
        - pluginRef: ttft-aware-scorer
          weight: 1.0
        - pluginRef: max-score-picker
        - pluginRef: body-field-to-header
        - pluginRef: base-model-to-header
    datalayer:
      extractors:
      - pluginRef: ttft-percentile-extractor
      datasources:
      - pluginRef: model-config-datasource
```

The scorer requires the `ttft-percentile-extractor` in `datalayer.extractors`, and a model
list (here via `model-config-datasource`) so model selection has candidates.

## Possible Enhancements

### Score-proportional picker

`max-score-picker` sends 100% of traffic to the single winner, turning every small
score difference into a full traffic flip. This causes oscillation: the best model
overloads, all traffic switches to the other, the first model drains and wins again.
`score-proportional-picker` eliminates this by routing probabilistically:
```
P(model i) proportional to score_i^(1/T)    # T = temperature, default 1.0
```
At T = 1.0, a model scoring 0.8 vs 0.2 receives ~80% vs 20% of requests.

### Prompt-length-aware floor

P10Low is estimated from the fastest observed completions, which tend to be short-prompt
requests. For a long-prompt request, the hardware-floor prefill time is intrinsically higher,
so the scorer under-predicts TTFT even at zero queue depth.

A more accurate floor would scale with the incoming prompt token count:
```
P10Low(tokens) = base_prefill + tokens × prefill_rate
```
where `base_prefill` and `prefill_rate` are fit from observations bucketed by prompt length.
This matters most when the workload has high prompt-length variance (e.g. RAG pipelines
mixing short queries with large context windows).
