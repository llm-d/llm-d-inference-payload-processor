# Max Score Picker

Selects the model with the highest score calculated during the scoring phase.

It is registered as type `max-score-picker` and runs as a modelselector picker.

## What it does

1.  Receives a list of `ScoredModel` candidates.
2.  Shuffles the list in-place to ensure random tie-breaking when multiple models share the same maximum score.
3.  Sorts the candidates by score in descending order.
4.  Returns the top model candidate.

## Behavioral Intent

This picker maximizes the adherence to scoring objectives (e.g., lowest cost, lowest TTFT, cache hit). However, it is susceptible to **hot-spotting** if many concurrent requests produce identical scores for the same model (e.g., identical prompts targeting a specific cache hit).

## Inputs consumed

- Consumes the list of `ScoredModel` results from the scoring phase.

## Configuration

The plugin has no config.
