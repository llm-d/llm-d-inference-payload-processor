# Request Metadata Extractor

**Type:** `request-metadata-extractor`

A datalayer extractor that tracks in-flight request counts and token sums per model. Downstream plugins (e.g. `inflight-requests-scorer`) read these counters to make load-aware scoring decisions.

## What it does

1. On each **request event**, increments the in-flight request count and adds `max_tokens` to the token sum for the model named in the request body's `model` field.
2. On each **response event**, decrements the request count and subtracts the token sum (floored at zero).
3. Writes a `RequestMetadataCount` struct (`Requests`, `Tokens`) to the model's attribute store under the key `request-metadata`.

Models are identified by the `model` field in the request body. Events with a missing or non-string `model` field are silently skipped.

## Configuration

### Parameters

None.

### Datalayer wiring

```yaml
datalayer:
  extractors:
    - pluginRef: request-metadata-extractor
```

## Outputs produced

| Where | Contents |
|-------|----------|
| Datastore attribute `request-metadata` | `RequestMetadataCount{Requests, Tokens}` per model |

## Known limitations

Counters can leak if a request fails without a corresponding response event (e.g. connection drop, upstream error). The call site should fire a synthetic response event in its error path to keep counts accurate.
