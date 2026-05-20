# Weighted Random Picker

Selects model randomly, where the probability of a model being selected is proportional to its score.

It is registered as type `weighted-random-picker` and runs as a modelselector picker.

## What it does

1.  Receives a list of `ScoredModel` candidates.
2.  If all candidates have a score of zero or less, it delegates to the `random-picker` for uniform selection.
3.  Uses the **A-Res (Algorithm for Reservoir Sampling)** algorithm to perform mathematically correct weighted random sampling.
    - Generates a random key for each endpoint based on its score: $key_i = U_i^{(1/w_i)}$ where $U_i$ is a random number in $(0,1)$ and $w_i$ is the endpoint's score.
    - Selects the candidates with the largest keys.
4.  Returns the top candidate.

## Behavioral Intent

This picker resolves the trade-off between `max-score-picker` and `random-picker`. It prefers higher-scoring model while maintaining exploration and avoiding extreme hot-spotting. 

## Inputs consumed

- Consumes the list of `ScoredModel` results and utilizes the `Score` value.

## Configuration

The plugin has no config.
