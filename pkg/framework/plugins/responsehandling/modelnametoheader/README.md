# Model Name to Header

**Type:** `model-name-to-header`

A response headers processor that echoes the model name selected during request processing back to the client as a response header.

## How it works

1. Reads the selected model name from CycleState (the key written by the `model-selector` request processor).
2. Sets the configured response header to the selected model name.
3. Runs during the response-headers phase, so it works for both streaming and buffered responses.

If no model was selected (CycleState key is missing), the header is not set.

## Configuration

```yaml
plugins:
- name: model-name-to-header
  type: model-name-to-header
  parameters:
    headerName: X-Gateway-Model-Name  # optional
```

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `headerName` | string | no | `X-Gateway-Model-Name` | The response header name to set |

### Profile wiring

The plugin must appear in the `response` section of the profile:

```yaml
profiles:
- name: default
  plugins:
    response:
    - pluginRef: model-name-to-header
```

## Inputs consumed

- CycleState key `model-selector/selected-model`, written by the `model-selector` request processor.
