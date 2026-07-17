# Cost-Aware Scorer

**Type:** `cost-scorer`

A model-selector scorer that ranks candidate models by input-token price. Lower-priced models receive higher scores.

## How it works

- Reads `pricing.TokenPricesAttributeKey` from each candidate model's attributes.
- Scores using inverted sum normalization: `score = 1.0 - price / sum(prices)`.
- If only one model is present, it receives a neutral score of 0.5.
- If all models have zero price, each receives a score of 1.0.
- Models without a `TokenPrices` attribute are treated as zero-priced.

The scorer ranks by input-token price alone; the actual per-request cost also depends on output-token price, but ordering by input price is assumed to produce the same ordering as total cost.

## Configuration

### Parameters

None.

### Profile wiring

```yaml
profiles:
- name: default
  plugins:
    request:
    - pluginRef: model-selector
    - pluginRef: cost-scorer
      weight: 1.0
```

## Inputs consumed

- `pricing.TokenPricesAttributeKey` attribute on each candidate model, populated by the `model-config-datasource` plugin from the `pricing` section of its config file.
