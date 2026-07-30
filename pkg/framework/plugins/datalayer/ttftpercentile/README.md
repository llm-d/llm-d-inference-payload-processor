# TTFT Percentile Extractor

Data-layer extractor (`ttft-percentile-extractor`) that observes each model's actual TTFTs and
publishes a small `TTFTPercentileMetrics` snapshot to the model's attribute store every
`intervalDuration`. The [TTFT-aware scorer](../../modelselector/scorer/ttftaware/README.md)
consumes that snapshot to predict TTFT under load; this package only measures and summarises — it
carries no prediction formula of its own.

## Motivation

Our [TTFT-aware scorer](../../modelselector/scorer/ttftaware/README.md) routes each request to
whichever model pool is predicted to give the best TTFT under its *current* load (e.g. offloading
from a saturated small model to an idle larger one). To predict TTFT at an arbitrary load, it
interpolates between a handful of anchor points: an estimated load-invariant floor, plus TTFT/inflight
pairs at two operating percentiles. This extractor exists to measure and publish exactly those anchor
points, so measurement and prediction stay in separate packages.

## Published metrics

| Field | Meaning |
|---|---|
| `Requests` | current inflight request count for the model |
| `P10LowTTFT` | load-invariant service floor (see below) |
| `P10TTFT` | 10th percentile TTFT over the short window (floor fallback) |
| `LowTTFT`, `HighTTFT` | TTFT at the low / high operating percentiles (default P25 / P50) — the scorer's two anchors |
| `InflightAtLow` | average `inflight_at_dispatch` in the band around `lowPercentile` |
| `InflightAtHigh` | average `inflight_at_dispatch` in the band around `highPercentile` |
| `RecentN` | observation count in the capped short window |
| `Observations` | cumulative observations that have fed the floor (gates `Floor()`) |
| `MinRequests` | trust threshold, copied from config so the scorer needs no separate param |

Two internal structures produce these:

**Short window** (`maxObservationAge` / `maxRequests`, kept responsive) — a value-sorted, capped,
age-bounded snapshot of recent observations. It yields `P25`, `P50`, `P10`, and the two banded
inflight averages. Averaging inflight over a percentile band rather than a single observation
stabilises the operating-point estimate.

**Bucket history** (long-term) — produces the floor.

## The floor (`P10Low`)

Every TTFT decomposes as `TTFT = prefill_time + queue_wait`. Prefill time does not change with queue
depth, so a low percentile of TTFT recovers the hardware-bound prefill floor. Computing it robustly:

- once per `bucketDuration`, record the **P10 TTFT of that bucket** into a bounded history;
- `P10Low` is the **P10 of that history**;
- when the history is full (`bucketHistorySize` entries) the **smallest and largest** entries are
  evicted — not the oldest — so a single anomalously fast or slow bucket never sticks. The history
  is already value-sorted to take the percentile, so the ends are its min and max.

Because the history spans both idle and busy buckets, taking a low percentile of it locks onto the
idle buckets (the true floor) instead of drifting up with recent load — it is load-invariant.
The `bucketHistorySize` entries are evicted by value (min/max), not by age, which favours a stable floor over fast adaptation.

### Sample guard

`Floor()` returns **0 until at least `MinRequests` observations have fed it** (`Observations >=
MinRequests`), and only then the real `P10Low` (or `P10` before the history fills). A pool with a
handful of observations publishes a floor that is percentile-noise from cold-start requests — for
example a genuine idle TTFT of ~0.037s can read as 0.56s from 2–3 samples. Returning that would make
a barely-observed pool look confidently slow. Returning 0 instead makes the scorer treat it as
**cold** (does not compete on a poisoned value); the scorer's exploration then sends it calibration
probes until it crosses `MinRequests` and the floor becomes valid. The guard is cumulative, so a
pool that has already calibrated does not re-cold-start when it briefly goes idle.

## Parameters

| Parameter | Default | Description |
|---|---|---|
| `intervalDuration` | 1s | How often the snapshot is recomputed and published |
| `maxObservationAge` | 3m | Time bound for the short window (P25 / P50 / P10) |
| `maxRequests` | 100 | Cap the short window to the most recent N observations |
| `minRequests` | 10 | Minimum observations before the floor and operating point are trusted |
| `lowPercentile` | 25 | Low operating-anchor percentile (published as `LowTTFT` / `InflightAtLow`) |
| `highPercentile` | 50 | High operating-anchor percentile (`HighTTFT` / `InflightAtHigh`); must satisfy `0 < low < high < 100` |
| `windowSize` | 5000 | Ring buffer capacity (~200 KB per model) |
| `bucketDuration` | 1m | Window for each floor-history entry's P10; keep `<= maxObservationAge` |
| `bucketHistorySize` | 1000 | Per-bucket P10s kept for the floor; smallest/largest evicted by value when full (bounds memory, smooths the floor — not an age horizon) |

## Configuration

The extractor is wired under `datalayer.extractors`:

```yaml
- type: ttft-percentile-extractor
  parameters:
    intervalDuration: 1s
    windowSize: 5000
    maxObservationAge: 3m
    maxRequests: 100
    minRequests: 20
    bucketDuration: 1m
    bucketHistorySize: 1000
```

See the [scorer README](../../modelselector/scorer/ttftaware/README.md) for an end-to-end pipeline
that wires this extractor together with the scorer, a picker, and a model list.
