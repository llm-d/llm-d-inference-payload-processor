# Body Field to Header

**Type:** `body-field-to-header`

A request processor that extracts a named field from the JSON request body and sets its value as an HTTP header. This is the generic mechanism for exposing body fields to Envoy routing rules.

## How it works

1. Reads the configured `fieldName` from the request body.
2. If the field is present and non-empty, sets the configured `headerName` header to its string value.
3. If the field is missing or empty, the request passes through unchanged.

## Configuration

```yaml
plugins:
- name: model-to-header
  type: body-field-to-header
  parameters:
    fieldName: model
    headerName: X-Gateway-Model-Name
```

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `fieldName` | string | yes | — | The request body field to extract |
| `headerName` | string | yes | — | The header to set with the extracted value |

## Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `ipp_body_field_to_header_success_total` | Counter | Field found and header set |
| `ipp_body_field_to_header_missing_total` | Counter | Field missing or empty, request passed through |
