# CostGuard Scorer

**Status:** coming soon — this folder is a placeholder for the upcoming feature implementation to address [Issue 214](https://github.com/llm-d/llm-d-inference-payload-processor/issues/214).

## Overview

CostGuard is a model scorer for the [ModelSelector framework](https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/docs/proposals/043-model-selection-framework). The scorer minimises the **actual** end-to-end cost of inference, not just the input-token price.

The key insight is that input-token price is a poor proxy for total cost. A reasoning model with cheaper input tokens may generate far more output tokens than a model with more expensive input tokens. Because output tokens are priced significantly higher than input tokens, a verbosely generating model can end up costing much more in practice. CostGuard learns from observed request costs at runtime and routes to the model that is cheapest *in practice*, considering both typical requests and expensive outliers.

This is the successor to the [`costaware`](../costaware) scorer, which ranks models by input-token price alone.

## How it works

CostGuard treats each candidate model as an arm in a Multi-Arm Bandit problem. It applies an $\epsilon$-Greedy exploration-exploitation strategy:

- **Exploitation** (most of the time): route to the model with the lowest observed cost rank.
- **Exploration** (with probability $\epsilon$): route to a randomly chosen model to gather additional cost observations and make sure the scorer does not stale.

### Per-model cost rank

For each model, CostGuard maintains a [t-digest](https://github.com/tdunning/t-digest) over the actual costs observed when that model served a request. The rank of a model is:

$$rank = TrimmedMean(0, \alpha) + \lambda \cdot CTE(\alpha)$$

- **TrimmedMean(0, $\alpha$)** — the mean of the bottom $\alpha$ fraction of observed costs (the body of the distribution).
- **CTE($\alpha$)** — the Conditional Tail Expectation above the $\alpha$-quantile (the expected value of a draw from the tail).
- **$\alpha$** — the quantile that separates body from tail (e.g., 0.95 for the 95th percentile).
- **$\lambda$** — a penalty weight on the tail contribution (default: 1).

This formulation simultaneously optimises for typical cost *and* penalises models that occasionally produce very expensive responses.

### Score function

Models are scored in [0, 1] using a temperature-scaled sigmoid:

$$score(m) = \frac{1}{(1 + exp(\beta \cdot (rank_{m} − M)))}$$

- **M** — median rank across all candidate models.
- **$\beta = \frac{1}{\sigma}$** — temperature derived from the standard deviation $\sigma$ of ranks.

This self-calibrating sigmoid automatically widens score separation when model costs differ greatly and compresses it when they are close, preserving discriminative power regardless of the model set.

Models under exploration receive a score **1**; all other models receive a score **0.5**.

**Note:** assigning score **1** to a target model and **0.5** to all others is a *probabilistic preference*, not a guarantee that exploration will succeed. When CostGuard is composed with other weighted scorers in the same pipeline, the final selection is determined by the weighted sum of all scorer outputs. A strong signal from another scorer can outweigh CostGuard's preference and route the request to a different model.

### Lifecycle

CostGuard operates in a **time window (epoch)**. Within each epoch:

1. **Exploration** — with probability $\epsilon$ a random under-explored model is selected (score = 1); all others score 0.5. A model is under-explored if it does not have a minimal number of actual inference cost samples to calculate the $\alpha$-percentile with sufficient statistical fidelity (i.e., a sufficiently narrow confidence interval).
2. **Exploitation** — for all models that have been explored, the rank-based sigmoid scoring applies with probability $1-\epsilon$, ties broken arbitrarily.

At the end of each epoch the t-digests are frozen and a new one is started for the next epoch.

### Cost data flow

CostGuard relies on two supporting components that feed it observed costs:

1. **`modelconfigcollector`** — collects input and output token prices per model from the data layer.
2. **`requestcostdata` extractor** — reads `usage.prompt_tokens` and `usage.completion_tokens` from each completed response, converts them to a USD cost using the token prices, and stores the result in the data layer in the model's t-digest. This path is non-blocking.

CostGuard reads the t-digest cost inference costs from the data layer's `AttributeMap` and computes ranks and scores on the fly at scoring time.

## Configuration

CostGuard exposes five main knobs and two advanced knobs. All have sensible defaults, and the scorer works out of the box without any configuration.

| Parameter | Type | Default | Description |
| --- | --- | --- | --- |
| `epsilon` | float, [0, 1] | `0.1` | Probability of random exploration on each request. Setting it to `1.0` forces full random behavior (with replacement). |
| `alpha` | float, [0, 1] | `0.95` | Quantile that separates the body of the cost distribution from the tail. For example, `0.95` means the top 5 % of observed costs are treated as the tail. |
| `lambda` | float, >= 0 | `1.0` | Penalty weight applied to the tail cost (CTE). Increase the penalty to penalise models with expensive outlier responses more aggressively. |
| `windowDuration` | duration string | `"2h"` | Length of each epoch. The t-digest is reset at the end of each window. |
| `w` | float, [0, 1] | `0.03` | This is quantile margin of error. The true $\alpha$-percentile of the cost distribution of the model is between $[X - w, X + w]$. In other words, if $\alpha = 0.95$ and $w = 0.03$, then at a $95\%$ confidence level, the true percentile `95` lies between `P92` and `P98` of the *observed* values. Always calculated at the $95\%$ confidence level. |

The scorer automatically determines the minimal number of samples required for exploring the model given the above parameters. A user should be careful in setting `w` and `windowDuration`.

Making the `w` too small will require too many observations to explore a model, because the number of observations is inversely proportional to the square of `w`. In the above example, around `200` observations are required.

Making the `windowDuration`. Too short or too long might hurt the quality of scoring.

**TODO**: add a guide on parameter tuning.  

### Example

```yaml
scorers:
  - type: costguard
    name: my-costguard
    config:
      epsilon: 0.05                # 5 % exploration probability
      alpha: 0.95                  # 95th-percentile tail boundary
      lambda: 1.5                  # penalise tail costs 50 % more
      windowDuration: "30m"        # reset cost history every 30 minutes
      percentileMarginError: 0.05  # the confidence interval width
```

## What CostGuard does not do

- **Budget caps** — CostGuard does not enforce per-request or per-batch cost limits.
- **Accuracy optimisation** — the scorer is cost-only; it assumes all candidate models in the set are accuracy-compatible.
- **Adapting to fast cost drift** — in the initial version, all samples within an epoch are weighted equally.
- **Optimal regret guarantees** — $\epsilon$-Greedy exploration is chosen for simplicity and robustness over theoretically optimal strategies.

## Related components

| Component | Path |
|---|---|
| `costaware` scorer (input-price baseline) | [`pkg/framework/plugins/modelselector/scorer/costaware`](../costaware) |
| `modelconfigcollector` datalayer plugin | [`pkg/framework/plugins/datalayer/modelconfigcollector`](../../../datalayer/modelconfigcollector) |
| `ModelSelector` framework proposal | [`docs/proposals/043-model-selection-framework`](https://github.com/llm-d/llm-d-inference-payload-processor/tree/main/docs/proposals/043-model-selection-framework) |
