# Median-TTFT Scorer

Routes each request to the model with the lowest predicted TTFT under current load.

## Equations

Every TTFT decomposes as `TTFT = prefill_time + queue_wait`.

**P10Low** — queue-free service floor (10th percentile, inflight_at_dispatch ≤ 2):
```
P10Low ≈ prefill_time
```
High-inflight observations are excluded, so P10Low is immune to burst flooding.
A 10-min window keeps the estimate alive during sustained overload.

**Capacity** — updated only when `P50/P10Low ∈ [1.5, 3.0]` (balanced zone):
```
capacity = inflightAtP50 × P10Low / (P50 − P10Low)
```
Derived from `P50 - P10Low = P10Low * inflight/capacity`. where `P50 - P10Low` is estimating the waiting time.
`inflightAtP50` is the `inflight_at_dispatch` of the observation whose TTFT landed at
P50. Below 1.5 the denominator is too noisy; above 3.0, P50 is contaminated by flooding; capacity is frozen in both cases.

**Scorer:**
```
loadRatio     = inflight / capacity
effectiveTTFT = P10Low × (1 + (loadRatio − 1))   when loadRatio > 1
              = P10Low                           otherwise
score         = (maxTTFT − effectiveTTFT) / (maxTTFT − minTTFT)
```
Within capacity: `effectiveTTFT = P10Low`, the queue-free baseline.
At 2× capacity: `effectiveTTFT = 2 × P10Low` — exactly what the queue model predicts.
Unobserved models score 1.0 (cold start) or 0.5 (idle alongside observed peers).

## Why it should work physically

Think of the model as C parallel slots, each finishing a request in P10Low seconds.
With N requests in the system, the server clears them at a rate of C every P10Low, so
draining the current backlog takes `(N / C) × P10Low`. That drain time is the queue
wait a new request sees:

```
TTFT = P10Low + (N / C) × P10Low = P10Low × (1 + N / C)
```

Rearranging for C at the observed operating point `(inflightAtP50, P50)`:

```
capacity C = inflightAtP50 × P10Low / (P50 − P10Low)
```

The same model gives the scorer's penalty directly: when `inflight > C`, the predicted
drain time exceeds P10Low, so `effectiveTTFT = P10Low × (inflight / C)`.

## Possible Enhancements
### prompt-length awareness

Prefill time scales roughly linearly with token count, but the scorer currently treats
all requests identically regardless of prompt size. The fix is to normalise TTFT by
character count in the tracker (`rate = TTFT / chars`) and scale at score time:
```
effectiveTTFT = P10Low_rate × request_chars × max(1, inflight/capacity)
```
This makes the scorer aware of the current request's weight without requiring a
model-specific tokeniser; character count (≈ tokens / 4) is a sufficient proxy.

### Score-proportional picker

`max-score-picker` sends 100% of traffic to the single winner, turning every small
score difference into a full traffic flip. This causes oscillation: the best model
overloads, all traffic switches to the other, the first model drains and wins again.
`score-proportional-picker` eliminates this by routing probabilistically:
```
P(model i) ∝ score_i^(1/T)    # T = temperature, default 1.0
```
At T = 1.0, a model scoring 0.8 vs 0.2 receives ≈ 80% vs 20% of requests.
