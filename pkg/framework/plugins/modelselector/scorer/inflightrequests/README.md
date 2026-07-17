# In-Flight Requests Scorer

**Type:** `inflight-requests-scorer`

A model-selector scorer that favors the least-loaded model by in-flight request count.

## How it works

- Reads the `request-metadata` attribute from each candidate model's attribute store (populated by the `request-metadata-extractor` plugin).
- Scores using min-max normalization: `score = (max - count) / (max - min)`.
- The least loaded model scores 1.0; the most loaded scores 0.0.
- If all models have the same count, all score 1.0.
- Models without the attribute are treated as idle (0 in-flight requests).

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
    - pluginRef: inflight-requests-scorer
      weight: 1.0
```

### Prerequisites

The `request-metadata-extractor` must be configured as a datalayer extractor so that in-flight counts are available:

```yaml
datalayer:
  extractors:
    - pluginRef: request-metadata-extractor
```

## Inputs consumed

- `request-metadata` attribute on each candidate model, written by the `request-metadata-extractor` plugin.
