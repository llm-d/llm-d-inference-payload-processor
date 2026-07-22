# TTFT-Aware Scorer

Routes each request to the model with the lowest predicted TTFT under current load.

It consumes the per-model snapshot published by the
[TTFT percentile extractor](../../../datalayer/ttftpercentile/README.md) — the service floor
`P10Low`, the operating points `P25` / `P50`, and their banded inflight averages `inflightAtP25` /
`inflightAtP50` — and turns them into a prediction and a score. See the extractor README for how
those inputs are measured.

## Prediction — `effectiveTTFT`

The predicted TTFT for a request arriving now is a line through the high operating point
`(inflightAtP50, P50)` and a low anchor that blends between the in-cloud point
`(inflightAtP25, P25)` and the load-free floor `(0, P10Low)`:

```
w             = clamp((inflightAtP50 - inflightAtP25) / anchorGapScale, 0, 1)
lowInflight   = w * inflightAtP25
lowTTFT       = w * P25 + (1 - w) * P10Low
effectiveTTFT = lowTTFT + (inflight - lowInflight) * (P50 - lowTTFT) / (inflightAtP50 - lowInflight)
```

Clamped to `>= P10Low`.

### Model states

Each candidate is in one of three states, from its published metrics:

- **cold** — `Floor() == 0` (never observed, or fewer than `minRequests` observations, so the floor
  is not yet trustworthy). No operating point; seeded optimistically at the best observed TTFT.
- **seed** — has a floor but is not yet calibrated (`RecentN < minRequests`, or no inflight
  operating point). Predicts at the floor.
- **trusted** — calibrated: `RecentN >= minRequests`, `inflightAtP50 > 0`, `P50 > floor`. Uses the
  full blended prediction above.

## Score

```
score = (maxTTFT - effectiveTTFT) / (maxTTFT - minTTFT)
```

Lowest predicted TTFT scores highest. Cold models seed at `minTTFT`; if every model is cold, all
score 1.0.

### Exploration

An under-observed pool can be starved: competing against a calibrated pool it may score low and
never win the traffic it needs to calibrate, so it stays under-observed forever. `explorationRate`
breaks that loop. Each under-observed pool is flipped **independently** per request:

- **heads** (probability `explorationRate`): that pool's final score is forced to `1.0` — the top —
  so the picker sends it a guaranteed calibration probe.
- **tails** (probability `1 - explorationRate`): that pool is suppressed to `0` so only calibrated
  pools compete — but only when a calibrated pool exists to take the traffic.

`explorationRate == 0` disables exploration (every request goes to the winner). The override is
applied to the **final score only** — it never feeds the min/max normalisation, so a probe cannot
distort the trusted pools' scores.

Because each under-observed pool is flipped independently, every cold pool is probed at its own
`explorationRate` regardless of how many others are cold — so the fraction of requests that explore
*something* grows with the number of cold pools.
This trades a larger exploration budget for guaranteed per-pool probe coverage.

Together with the extractor's floor sample guard, this reproduces automatically what a manual warmup
would do by hand: a brand-new pool reads as cold → receives probes → accumulates observations →
crosses `minRequests` → competes on its true latency.

## Why the blend works physically

When more requests are in flight, a new request waits longer in the queue, so TTFT rises with
inflight — and it rises *faster than linearly* as the server approaches saturation (queueing).

The scorer draws a line through two points it has actually observed:

- fast requests ran at lower load: `(inflightAtP25, P25)`
- median requests at higher load: `(inflightAtP50, P50)`

Because both anchors sit *inside* the observed load cloud, the line follows the **local slope** of
that convex curve — so it does not systematically under-predict the way a single chord from a
synthetic zero-load floor does.

At low load the two anchors collapse together (every request sees similar, low inflight), so their
slope is ill-defined. The blend weight `w` handles this smoothly: as the inflight gap shrinks,
`w → 0` slides the low anchor down to `(0, P10Low)`, recovering the stable floor chord. There is no
threshold and no discontinuity, and the denominator `inflightAtP50 - w*inflightAtP25` can never
collapse. The only knob, `anchorGapScale`, is a numerical-conditioning scale (how much inflight
separation counts as "well separated"), not a fitted parameter.

## Parameters

| Parameter | Default | Description |
|---|---|---|
| `explorationRate` | 0.0 | Per-pool probability that an under-observed pool is probed on a given request (flipped independently per pool). 0 = all traffic to the trusted winner; 0.1 = each cold pool probed on ~10% of requests. |
| `anchorGapScale` | 2.0 | Inflight separation (`inflightAtP50 - inflightAtP25`) at which the prediction fully trusts the in-cloud secant; below it the low anchor blends toward the floor chord. Must be > 0. |

The scorer also reads `minRequests` from the extractor's published metrics; it is configured on the
[extractor](../../../datalayer/ttftpercentile/README.md), not here.

## Example configuration

An end-to-end Helm values override wiring the scorer together with the TTFT extractor, the
model-config datasource, and a picker:

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

The scorer requires the `ttft-percentile-extractor` in `datalayer.extractors`, and a model list
(here via `model-config-datasource`) so model selection has candidates.

## Possible enhancement — score-proportional picker

`max-score-picker` sends 100% of traffic to the single winner, turning every small score difference
into a full traffic flip. This causes oscillation: the best model overloads, all traffic switches to
the other, the first model drains and wins again. `score-proportional-picker` eliminates this by
routing probabilistically:

```
P(model i) proportional to score_i^(1/T)    # T = temperature, default 1.0
```

At T = 1.0, a model scoring 0.8 vs 0.2 receives ~80% vs 20% of requests.
