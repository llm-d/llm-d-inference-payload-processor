# TTFT-Aware Scorer

Routes each request to the model with the lowest predicted TTFT under current load.

## Equations

Every TTFT decomposes as `TTFT = prefill_time + queue_wait`.

**P10Low** — hardware-bound service floor:

Computed from a long window (default 1h) using all observations regardless of inflight level:
1. Find the P10 TTFT threshold across all observations in the window
2. Take the P10 of only the observations at or below that threshold (~P1 of all)

This isolates the fastest requests in the window — those with the least queue wait — without
requiring the model to have idle periods. P10Low is hardware-bound and stable: prefill time
does not change with queue depth, concurrency level, or scale events.

**P50 and inflightAtP50** — current operating point (short window, default 3m / 100 requests):
```
P50           = 50th percentile TTFT
inflightAtP50 = average inflight_at_dispatch of observations in the P40-P60 band
```
The short window keeps P50 responsive to current load. Averaging over a band rather than
a single observation makes inflightAtP50 more stable.

**effectiveTTFT** — predicted TTFT for a request arriving now:
```
effectiveTTFT = P10Low + inflight x (P50 - P10Low) / inflightAtP50
```
Falls back to P10Low when P50 is not yet available or equals the floor.

**Score:**
```
score = (maxTTFT - effectiveTTFT) / (maxTTFT - minTTFT)
```
Under-observed models (UNOBSERVED or SEED state) receive an optimistic high score.
With `explorationRate > 0`, that high score is suppressed to 0 with probability
`(1 - explorationRate)` so only ~`explorationRate` fraction of requests are routed
to the under-observed model for calibration probing.

## Why it works physically

When more requests are in flight, a new request has to wait longer in the queue before
the model processes it. The longer the queue, the higher the TTFT. This wait grows
roughly in proportion to the number of in-flight requests.

The scorer draws a straight line through two points it has actually observed:

- when there is no queue (`inflight = 0`): TTFT = P10Low (just the raw prefill time)
- at the recent median load (`inflight = inflightAtP50`): TTFT = P50

It then reads off that line at the current inflight to predict what the next request
will wait. No fitting, no tunable parameters — just two observed points.

## Parameters

### Scorer (`ttft-aware-scorer`)

| Parameter | Default | Description |
|---|---|---|
| `explorationRate` | 0.0 | Fraction of requests routed to under-observed models for calibration probing. 0 = all requests go to the trusted winner; 0.1 = ~10% probe the under-observed model. |

### Extractor (`ttft-percentile-extractor`)

| Parameter | Default | Description |
|---|---|---|
| `maxObservationAge` | 3m | Time bound for the short window (P50 / P10) |
| `maxRequests` | 100 | Cap the short window to the most recent N observations |
| `minRequests` | 10 | Minimum capped-window count before the scorer trusts the formula |
| `lowLoadWindowAge` | 1h | Window for two-level P10Low (long = stable hardware floor) |
| `floorInterval` | 1m | How often P10Low is recomputed (slow-moving floor; cheaper than every interval) |
| `windowSize` | 5000 | Ring buffer capacity (~200 KB per model) |

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
      # effectiveTTFT = P10Low + inflight x (P50 - P10Low) / inflightAtP50
      # line through (0, P10Low) and (inflightAtP50, P50), extrapolated linearly
      parameters:
        explorationRate: 0.1          # 10% of requests probe under-observed models; 0 = disabled
    - type: max-score-picker
    - type: ttft-percentile-extractor
      parameters:
        intervalDuration: 1s
        windowSize: 5000
        maxObservationAge: 3m
        lowLoadWindowAge: 1h
        maxRequests: 100
        minRequests: 20
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
