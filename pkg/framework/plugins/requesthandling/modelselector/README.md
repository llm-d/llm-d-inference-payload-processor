# Model Selector

**Type:** `model-selector`

A request processor that runs the model-selector pipeline (Filter, Score, Pick) to choose a target model for the current request.

## How it works

1. Reads all candidate models from the Datastore.
2. Runs the configured filter, scorer, and picker sub-plugins in sequence.
3. Writes the selected model name into both the request body's `model` field and the CycleState (under key `model-selector/selected-model`).
4. Returns an error if no candidate models are available or if the pipeline produces no result.

The pipeline is initially empty. Sub-plugins (filters, scorers, pickers) are wired in by the configuration loader based on the profile definition:

```yaml
profiles:
- name: default
  plugins:
    request:
    - pluginRef: model-selector
    - pluginRef: model-group-name-filter
    - pluginRef: cost-scorer
      weight: 0.5
    - pluginRef: inflight-requests-scorer
      weight: 0.5
    - pluginRef: max-score-picker
```

## Configuration

### Parameters

None. Sub-plugin configuration is handled via the profile's plugin list.

## Outputs produced

| Where | Contents |
|-------|----------|
| Request body `model` field | Overwritten with the selected model name |
| CycleState `model-selector/selected-model` | The selected model name (read by response plugins) |
