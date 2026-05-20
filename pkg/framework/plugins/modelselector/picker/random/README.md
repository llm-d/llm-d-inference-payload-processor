# Random Picker

Selects model uniformly at random, ignoring any scores calculated by scorer plugins.

It is registered as type `random-picker` and runs as a modelselector picker.

## What it does

1.  Receives a list of `ScoredModel` candidates.
2.  Shuffles the list in-place to randomize the order.
3.  Returns the top candidate from the shuffled list.

## Behavioral Intent

This picker provides strict uniform distribution of load across all available candidates. It ignores all scoring signals, making it immune to hot-spotting but unable to leverage optimization signals like cost or latency.

## Inputs consumed

- Consumes the list of `ScoredModel` results (but ignores the `Score` field).

## Configuration

The plugin has no config.
