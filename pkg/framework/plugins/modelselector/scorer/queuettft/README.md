# Median-TTFT Scorer

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

**P50 and inflightAtP50** — current operating point (short window, default 1m):
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

Think of the model as having C parallel slots, each taking P10Low seconds per request.
With N requests in flight, a new arrival waits for the backlog to drain:

```
TTFT = P10Low + (N / C) x P10Low = P10Low x (1 + N / C)
```

This is a straight line through `(0, P10Low)` with slope `P10Low / C`. The scorer anchors
this line at two observed points instead of estimating C explicitly:

- at `inflight = 0`: TTFT = P10Low (hardware floor, no queue)
- at `inflight = inflightAtP50`: TTFT = P50 (observed median operating point)

The formula extrapolates this line to the current inflight, giving the predicted TTFT for
a new request. No capacity variable, no regression, no tunable parameters.

## Parameters

| Parameter | Default | Description |
|---|---|---|
| `windowAge` | 1m | Window for P50 (short -- keeps P50 fresh and responsive) |
| `lowLoadWindowAge` | 1h | Window for two-level P10Low (long -- stable hardware floor) |
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
