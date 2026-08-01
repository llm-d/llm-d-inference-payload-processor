# TTFT-Aware Scorer

Routes each request to the model with the lowest predicted TTFT under current load.

It consumes the per-model snapshot published by the
[TTFT percentile extractor](../../../datalayer/ttftpercentile/README.md) — the service floor
`P10Low`, the operating points `LowTTFT` / `HighTTFT` (default P25 / P50, configured on the
extractor) and their banded inflight averages `InflightAtLow` / `InflightAtHigh` — and turns them
into a prediction and a score.

## Prediction — `predictedTTFT`

Every pool has a latency curve: how long a new request waits as a function of how many requests are
already in flight. We measure three points on it and interpolate.

| point | coordinates | meaning |
|---|---|---|
| **A** | `(0, P10Low)` | queue-free service time |
| **B** | `(InflightAtLow, LowTTFT)` | low operating point (default P25) |
| **C** | `(InflightAtHigh, HighTTFT)` | high operating point (default P50) |

**A** and **C** always define the curve. **B** is inserted between them only when
[admissible](#when-the-low-point-is-used), splitting it into two segments. Any single prediction
reads one segment, so it uses two of the three points. Every point is something the extractor observed.

```
if B admissible:
    if inflight < InflightAtLow:                 # segment A->B
        predictedTTFT = P10Low + inflight * (LowTTFT - P10Low) / InflightAtLow
    else:                                        # segment B->C, extended past C
        predictedTTFT = LowTTFT + (inflight - InflightAtLow) *
                        (HighTTFT - LowTTFT) / (InflightAtHigh - InflightAtLow)
else:                                            # segment A->C, extended past C
    predictedTTFT = P10Low + inflight * (HighTTFT - P10Low) / InflightAtHigh
```

The curve is continuous (both branches give `LowTTFT` at `InflightAtLow`), equals `P10Low` at zero
load, and is monotone non-decreasing given `P10Low < LowTTFT < HighTTFT` — which the admissibility
checks enforce. `predictedTTFT` is clamped to `>= P10Low` as a defensive guard.

**Why three points and not two.** TTFT rises *faster than linearly* with inflight as a pool
approaches saturation. A single chord from the floor to **C** cuts across that convex curve and
under-predicts in between; **B** lets the curve bend so the loaded segment follows the local slope
where the pool is actually operating.

**Why A→B is a segment of its own.** The B→C slope is measured where queueing dominates, so it is
steep. Running it backwards below **B** makes TTFT cross below the floor within a handful of
requests — a draining-but-still-loaded pool would be predicted at its idle latency and win every
decision it appeared in. Interpolating from `(0, floor)` matches how TTFT flattens as the queue
drains.

### When the low point is used

**B** is admissible only when all of these hold. They are conditions on a measured point, not
tuning knobs:

| check | condition | why |
|---|---|---|
| separated in load | `InflightAtHigh - InflightAtLow >= minInflightGap` | the B→C slope is `ΔTTFT / Δinflight`; if both points sit at the same load that denominator is noise and the slope is meaningless |
| ordered in latency | `HighTTFT > LowTTFT` | TTFT must rise with the percentile. If noise inverts them the slope goes negative and the *most* loaded pool scores best, feeding a saturated pool |
| above the floor | `LowTTFT > P10Low` | `P10Low` is a long-window statistic and `LowTTFT` a recent one, so after a drain the recent P25 can fall below it — which would tilt the A→B segment downwards |
| positive inflight | `InflightAtLow > 0` | keeps the A→B divisor safe; the gap check alone does not imply it |

When **B** is dropped the curve is the single floor chord **A → C**, which is well defined at any
load.


### Model states

- **cold** — `Floor() == 0` (never observed, or fewer than `minRequests` observations so the floor
  is not yet trustworthy). Seeded optimistically at the best observed TTFT.
- **seed** — has a floor but is not calibrated (`RecentN < minRequests`, or no inflight operating
  point). Predicts at the floor.
- **trusted** — `RecentN >= minRequests`, `InflightAtHigh > 0`, `HighTTFT > floor`. Uses the curve
  above.

## Score

```
score = (maxTTFT - predictedTTFT) / (maxTTFT - minTTFT)
```

Lowest predicted TTFT scores highest. Cold models seed at `minTTFT`; if every model is cold, all
score 1.0.

### Exploration

An under-observed pool can be starved: competing against a calibrated pool it may never win the
traffic it needs to calibrate. `explorationRate` breaks that loop — each under-observed pool is
flipped independently per request, and with probability `explorationRate` its final score is forced
to `1.0` so the picker sends it a probe; otherwise it is suppressed to `0`, but only when a
calibrated pool exists to take the traffic. The override applies to the **final score only**, so a
probe never distorts the trusted pools' normalisation.

Together with the extractor's floor sample guard this reproduces what a manual warmup would do: a
new pool reads as cold → receives probes → crosses `minRequests` → competes on its true latency.

## Parameters

| Parameter | Default | Description |
|---|---|---|
| `explorationRate` | 0.0 | Per-pool probability that an under-observed pool is probed on a given request. 0 = all traffic to the trusted winner. |
| `minInflightGap` | 2.0 | Minimum inflight separation between the operating points for the low one to be used as an anchor. Must be > 0. |
| `roundTTFTStep` | 0.0 | Quantize each prediction to a multiple of this many seconds before ranking (e.g. `0.01` = 10 ms). Pools landing in the same bucket tie and the picker splits them, instead of one winning on a difference too small to be meaningful. `0` = disabled. Must be >= 0. |

`minRequests` is read from the extractor's published metrics and configured
[there](../../../datalayer/ttftpercentile/README.md), not here.

## Configuration

```yaml
- type: ttft-aware-scorer
  parameters:
    explorationRate: 0.1
    minInflightGap: 2.0
```

The scorer requires `ttft-percentile-extractor` in `datalayer.extractors`, a model list (e.g. via
`model-config-datasource`) so model selection has candidates, and a picker.
