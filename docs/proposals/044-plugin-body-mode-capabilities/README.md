# Plugin Response Body Mode Capabilities

## Proposal Status
***Implementing***

## Summary

This proposal introduces a **response body mode capability declaration** for IPP plugins. Each response plugin declares what level of access it needs to the response body, enabling the framework to pre-compute per-profile whether response body buffering is needed. When no plugin in the selected profile needs the full response body, the framework passes each chunk through to the client immediately — enabling real-time streaming.

## Problem Statement

The ext_proc `response_body_mode` is set to `FULL_DUPLEX_STREAMED` in deployment. The IPP framework accumulates all response body chunks in memory before processing ([server.go](../../../pkg/handlers/server.go)), which means:

1. **Streaming is broken** — clients see all response chunks arrive at once after the full response is collected
2. **Memory is unbounded** — each concurrent request accumulates the full response body with no upper limit
3. **All plugins pay the cost** — even profiles where no plugin needs the response body force full buffering

## Design

### Response Body Mode Declaration

Plugins optionally implement `ResponseBodyRequirement` to declare their needs:

```go
type ResponseBodyMode int

const (
    BodyNotNeeded ResponseBodyMode = iota  // headers-only plugin
    BodyChunked                             // can process individual chunks (future: ChunkProcessor)
    BodyFull                                // needs complete body in memory
)

type ResponseBodyRequirement interface {
    ResponseBodyMode() ResponseBodyMode
}
```

Plugins that don't implement `ResponseBodyRequirement` default to `BodyFull` (backward compatible).

### Pre-Computation

After profiles are built in the config loader, the framework iterates each profile's `ResponsePlugins`:
- If **any** plugin returns `BodyFull` (or doesn't implement the interface) → `profile.NeedsResponseBuffering = true`
- Otherwise → `profile.NeedsResponseBuffering = false`

This is computed once at startup — no per-request overhead.

### Conditional Buffering in server.go

At runtime, after the `ProfilePicker` selects a profile for the request:

**When `NeedsResponseBuffering = true`** (current behavior):
- Accumulate all chunks: `responseBody = append(responseBody, chunk...)`
- On EndOfStream: call `HandleResponseBody(ctx, reqCtx, responseBody)`
- Response plugins get the full parsed body

**When `NeedsResponseBuffering = false`** (new streaming path):
- Each chunk is acked immediately via `ackResponseBodyChunk()`
- Envoy forwards the chunk to the client in real-time
- No accumulation, no `HandleResponseBody` call
- Event notification still fires on EndOfStream for metrics/observability

### Startup Logging

The framework logs per-profile buffering decisions at startup:
```
INFO  Profile response buffering computed  profile=default needsResponseBuffering=true bufferingPlugins=[api-translation]
INFO  Profile response buffering computed  profile=headers-only needsResponseBuffering=false bufferingPlugins=[]
```

Plugins that don't implement `ResponseBodyRequirement` get a warning:
```
INFO  Response plugin does not declare ResponseBodyRequirement, defaulting to BodyFull  profile=default plugin=legacy-plugin/legacy
```

## Implementation Phases

| Phase | Scope | Status |
|-------|-------|--------|
| 1 — Framework | `ResponseBodyMode`, `ResponseBodyRequirement` interface, pre-computation, conditional buffering, tests | **This PR** |
| 2 — ChunkProcessor | `ChunkProcessor` interface for `BodyChunked` plugins, per-chunk dispatch in server loop | Next PR |
| 3 — Plugin declarations | Existing plugins implement `ResponseBodyRequirement` (ODH repo) | Separate PR |

## Migration

Existing plugins that don't implement `ResponseBodyRequirement` default to `BodyFull`. No breaking changes — everything works exactly as before. Profiles with only legacy plugins will continue to buffer.

## Future: Dynamic Mode Override

Envoy ext_proc supports `mode_override` in responses. A future enhancement could override `response_body_mode` to `NONE` per-request when no response plugin in the selected profile needs the body at all. This is out of scope for the initial implementation.
