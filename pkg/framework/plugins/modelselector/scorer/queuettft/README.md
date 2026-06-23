# Queue-TTFT Scorer

Routes each request to the model with the lowest predicted TTFT under current load.

## Equations

Every TTFT decomposes as `TTFT = prefill_time + queue_wait`.

**P10Low** — load-invariant service floor:

Computed from a long window (default 1h) using all observations regardless of inflight level:
1. Find the P10 TTFT threshold across all observations in the window
2. Take the P10 of only the observations at or below that threshold

This isolates the fastest requests in the window — those with the least queue wait — without requiring the model to have idle periods. P10Low is invariant to load: it does not change with queue depth, concurrency, or scale events, because prefill time itself doesn't.

Short-prompt bias usually cancels: the scorer only ranks, so a shared bias is harmless.


**P50 and inflightAtP50** — current operating point (short window, default 3m):
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
Unobserved models score 1.0 (cold start) or 0.5 (idle alongside observed peers).

## Why it works physically

When more requests are in flight, a new request has to wait longer in the queue before
the model processes it. The longer the queue, the higher the TTFT. This wait grows
roughly in proportion to the number of in-flight requests.

The scorer draws a straight line through two points it has actually observed:

- when there is no queue (`inflight = 0`): TTFT = P10Low (just the raw prefill time)
- at the recent median load (`inflight = inflightAtP50`): TTFT = P50

It then reads off that line at the current inflight to predict the next request's TTFT.

## Parameters

| Parameter | Default | Description |
|---|---|---|
| `windowAge` | 3m | Window for P50 (short -- keeps P50 fresh and responsive) |
| `lowLoadWindowAge` | 1h | Window for two-level P10Low |
| `windowSize` | 5000 | Ring buffer capacity (~200 KB per model) |
| `minObservations` | 3 | Minimum observations required to compute any percentile |

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
requests. For a long-prompt request, the prefill time is intrinsically higher, so the scorer under-predicts TTFT even at zero queue depth.

A more accurate floor would scale with the incoming prompt token count:
```
P10Low(tokens) = base_prefill + tokens × prefill_rate
```
where `base_prefill` and `prefill_rate` are fit from observations bucketed by prompt length.
This matters most when the workload has high prompt-length variance.
