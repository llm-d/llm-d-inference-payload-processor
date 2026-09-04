# Request Metadata Extractor

Tracks in-flight request counts and token sums per model, and publishes them to the model's
attribute store.

It is registered as type `request-metadata-extractor` and runs as a data-layer extractor.

## What it does

1. On a `RequestEventType` event, reads the request's `model` and `max_tokens` fields and
   increments that model's in-flight request count and token sum.
2. On a `ResponseEventType` event, reads the same fields from the originating request and
   decrements the counts, floored at zero.
3. Writes the updated `RequestMetadataCount{Requests, Tokens}` to the model's
   `request-metadata` attribute whenever a counter changes.

Events with no `model` field, or of any type other than `RequestEventType` /
`ResponseEventType`, are ignored.

## Behavioral Intent

Gives a cheap, always-current view of how many requests and tokens are currently in flight per
model, for consumers that want a load signal without querying upstream servers directly.

## Known limitations

Counters leak if a request fails without a corresponding `ResponseEventType` (a connection
drop, an upstream error, or context cancellation). The extractor has no timeout or reconciliation
path of its own; the call site that fires the original `RequestEventType` is expected to also
fire a synthetic `ResponseEventType` on its error/EOF path to keep counts accurate.

## Concurrency assumptions

`Extract` assumes it is called from a single goroutine (the `NotificationSource` event loop). It
keeps its counters in a plain, unsynchronized map. If parallel dispatch of `Extract` calls is
ever introduced, this needs a mutex around the counters and the datastore write.

## Configuration

None. The plugin has no configurable fields.

### Example plugin config

```json
{
  "type": "request-metadata-extractor",
  "name": "my-request-metadata"
}
```

## Inputs consumed

- `RequestEventType` events: `Request.Body["model"]` and `Request.Body["max_tokens"]`.
- `ResponseEventType` events: `Request.Body["model"]` and `Request.Body["max_tokens"]` from the
  originating request.

## Outputs produced

- `request-metadata` attribute on the model, of type `RequestMetadataCount{Requests, Tokens}`,
  updated on every request or response event that changes a counter.
