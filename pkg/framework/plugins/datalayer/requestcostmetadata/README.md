# Request Cost Metadata Extractor

Accumulates per-model cost samples from inference responses and publishes cost distribution snapshots (t-digests) to the model's `AttributeMap`. Enables scoring models by actual cost and actual cost observability.

It is registered as type `model-cost-extractor` and runs as a data-layer extractor.

## What it does

1. Extracts prompt and completion token counts from each response's usage metadata.
2. Looks up per-token pricing for the model from the model's attribute map.
3. Computes request cost = (prompt_tokens x input_price) + (completion_tokens x output_price).
4. Adds the cost sample to a per-model t-digest (compressed distribution).
5. Publishes a t-digest snapshot to the model's `CostDigest` attribute on the configured flush interval.

## Behavioral Intent

Provides a memory-efficient (constant space per model) view of the cost distribution across requests. Enables operators to track cost trends, detect anomalies, and drive actual cost-aware model selection.

## Inputs consumed

The extractor reads three pieces of information for each request:

1. **Model name** — From the inference request (e.g., `"gpt-4"`)
2. **Token usage** — From the response, how many tokens were used:
   - Input tokens (prompt): the user's input
   - Output tokens (completion): the model's response
3. **Pricing rates** — From model configuration, the cost per token:
   - Input token price (e.g., $0.005 per million tokens)
   - Output token price (e.g., $0.015 per million tokens)

Example inference response (OpenAI):

```json
{
  "model": "gpt-4",
  "usage": {
    "prompt_tokens": 150,
    "completion_tokens": 50
  }
}
```

## Outputs published

- Model's `CostDigest` attribute (updated on flush interval) — a t-digest snapshot with per-request costs

## Configuration

| Parameter | Type | Default | Description |
| --- | --- | --- | --- |
| `compression` | float | 200.0 | T-digest compression factor; higher values trade memory for accuracy. Must be > 0. |
| `flushIntervalDuration` | string | `"5s"` | Aggregation window before publishing a cost snapshot. Use `"0s"` to publish on every event. |

## Deployment

To enable the request cost metadata extractor, add the following to your `deploy/config/ipp-config.yaml`:

```yaml
plugins:
- type: model-cost-extractor
  parameters:
    compression: 200
    flushIntervalDuration: "5s"

datalayer:
  extractors:
  - pluginRef: model-cost-extractor
```

When the `name` field is omitted, the plugin receives a default name equal to its `type`. The `pluginRef` references the plugin by this name.
