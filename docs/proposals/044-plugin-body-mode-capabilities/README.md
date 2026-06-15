# Plugin Body Mode Capabilities

Author(s): @noyitz

## Proposal Status
***Proposed***

## Summary

This proposal introduces a **body mode capability declaration** mechanism for IPP plugins. Each plugin declares what level of access it needs to request and response bodies, enabling the framework to compute the optimal Envoy ext_proc `processing_mode` and validate it against the actual deployment configuration.

Today, `request_body_mode` and `response_body_mode` are global Envoy settings applied to all plugins uniformly. This forces a "most demanding wins" approach at the infrastructure level — if any plugin needs the full response body, streaming breaks for everyone. This proposal moves the decision to the plugin layer, where the framework can reason about requirements, warn on misconfigurations, and enable streaming-compatible plugins to coexist with body-dependent ones.

## Problem Statement

The ext_proc `response_body_mode` is set to `FULL_DUPLEX_STREAMED` in the default MaaS deployment. The IPP framework accumulates all response body chunks in memory before processing ([server.go#L96-L146](https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/pkg/handlers/server.go#L96)), which means:

1. **Streaming is broken** — clients see all response chunks arrive at once after the full response is collected, resulting in multi-second TTFT for long responses
2. **Memory is unbounded** — each concurrent request accumulates the full response body (`responseBody = append(responseBody, ...)`) with no upper limit. A 200K token streaming response is ~1MB in memory per request
3. **All plugins pay the cost** — even plugins that don't need the response body (e.g., `apikey-injection`) force full buffering because the mode is global

Different plugins have fundamentally different needs:

| Plugin | Request Body | Response Body | Can Process Chunks? |
|--------|-------------|---------------|-------------------|
| `body-field-to-header` | Needs full body (parse JSON field) | Not needed | No |
| `api-translation` | Needs full body (format conversion) | Needs full body (reverse conversion) | No |
| `external-metering` | Not needed | Needs usage from final SSE event | Yes — only reads final chunk |
| `apikey-injection` | Not needed | Not needed | N/A |
| `nemo-guard` | Needs full body (content inspection) | Needs full body (output inspection) | No |

## Design Principles

- Plugins declare capabilities, the framework decides behavior
- Existing plugins that don't declare capabilities continue to work unchanged (backward compatible)
- The framework validates but does not silently override — operators make the final decision
- Chunk processing is opt-in — plugins that can handle streaming declare it explicitly

## Definitions

- **BodyNeed** — what a plugin requires from a body (none, chunked, or buffered)
- **ChunkProcessor** — a plugin that can process individual response body chunks as they stream through
- **Effective mode** — the most demanding body need across all active plugins
- **Mode mismatch** — when the Envoy config doesn't match the computed effective mode

## Proposal

### 1. Body Mode Declaration Interface

Plugins optionally implement `BodyModeCapability` to declare their requirements:

```go
// BodyModeCapability allows plugins to declare what they need from request/response bodies.
// Plugins that don't implement this interface default to BodyFull (backward compatible).
type BodyModeCapability interface {
    BodyRequirements() BodyRequirements
}

type BodyRequirements struct {
    RequestBody  BodyNeed
    ResponseBody BodyNeed
}

type BodyNeed int

const (
    // BodyNotNeeded — plugin only works with headers, doesn't read or modify the body.
    BodyNotNeeded BodyNeed = iota

    // BodyChunked — plugin can process individual body chunks as they arrive.
    // Plugins declaring this MUST also implement ChunkProcessor.
    // Streaming-compatible: client sees chunks in real-time.
    BodyChunked

    // BodyFull — plugin needs the complete body in memory before processing.
    // NOT streaming-compatible: client waits for full response.
    BodyFull
)
```

The plugin declares what IT needs — it doesn't know about Envoy modes. The framework maps plugin needs to Envoy configuration:

| Highest plugin need | Envoy `body_mode` |
|--------------------|-------------------|
| All `BodyNotNeeded` | `NONE` |
| Any `BodyChunked` | `STREAMED` |
| Any `BodyFull` | `BUFFERED` |

### 2. Chunk-Aware Response Processing

Plugins that can handle streaming response chunks implement `ChunkProcessor`:

```go
// ChunkProcessor allows a plugin to process individual response body chunks
// without waiting for the full body. The framework calls ProcessResponseChunk
// for each chunk, then calls the standard ProcessResponse with the full
// accumulated body on the final chunk.
type ChunkProcessor interface {
    ProcessResponseChunk(ctx context.Context, cycleState *CycleState, chunk []byte, isFinal bool) ([]byte, error)
}
```

Constraints:
- Plugins declaring `BodyChunked` MUST implement `ChunkProcessor` — validated at startup
- `ProcessResponseChunk` returns modified bytes (to mutate the chunk forwarded to the client) or nil (pass through unchanged)
- The standard `ProcessResponse` is still called on the final chunk with the full accumulated body — this is backward compatible and allows the plugin to do aggregated work (e.g., emit a metering event with total token counts)

### 3. Effective Mode Computation

At startup, after all plugins are loaded, the framework computes the effective body mode:

```
Priority: NONE < CHUNKED < BUFFERED

For request body:
  If ANY plugin needs BUFFERED → effective = BUFFERED
  If ANY plugin needs CHUNKED  → effective = CHUNKED (STREAMED in Envoy terms)
  If ALL plugins need NONE     → effective = NONE

Same logic for response body, independently.
```

Mapping to Envoy ext_proc modes:

| Effective Need | Envoy `body_mode` | Behavior |
|---------------|-------------------|----------|
| `BodyNotNeeded` | `NONE` | Body not sent to ext_proc |
| `BodyChunked` | `STREAMED` | Chunks sent one at a time, acked immediately |
| `BodyFull` | `BUFFERED` | Full body buffered, sent as one message |

### 4. Startup Validation

The framework does **not** auto-override the Envoy configuration. It validates and reports:

```go
type ModeValidation struct {
    EffectiveRequestMode  BodyNeed
    EffectiveResponseMode BodyNeed
    Warnings              []string
    Errors                []string
}
```

**Validation rules:**

| Envoy Config | Effective Need | Result | Example |
|-------------|---------------|--------|---------|
| BUFFERED | CHUNKED | **WARNING**: streaming broken unnecessarily | "response_body_mode is BUFFERED but only STREAMED is needed — client streaming is disabled" |
| BUFFERED | NONE | **WARNING**: unnecessary overhead | "response_body_mode is BUFFERED but no plugin needs the response body" |
| NONE | CHUNKED | **ERROR**: plugin will fail | "external-metering requires response body but response_body_mode is NONE — token metering disabled" |
| NONE | BUFFERED | **ERROR**: plugin will fail | "api-translation requires full response body but response_body_mode is NONE" |
| STREAMED | BUFFERED | **ERROR**: plugin will get chunks, not full body | "api-translation requires full response body but response_body_mode is STREAMED" |
| STREAMED | CHUNKED | OK | ✅ |
| Config matches need | — | OK | ✅ |

**Example startup log output:**

```
INFO  plugin body mode analysis
      computed_request_mode=BUFFERED
      computed_response_mode=CHUNKED
      request_body_plugins=["body-field-to-header", "api-translation"]
      response_body_plugins=["external-metering"]
      chunk_capable=["external-metering"]

WARN  response_body_mode mismatch
      configured=FULL_DUPLEX_STREAMED recommended=STREAMED
      detail="Current mode accumulates all response chunks before forwarding.
              Use STREAMED for real-time streaming with token metering."

ERROR response_body_mode insufficient
      configured=NONE required=STREAMED
      plugin=external-metering
      detail="Plugin requires response body access for token extraction.
              Token metering will not work with current configuration."
```

### 5. Streaming Response Processing Flow

When `response_body_mode` is `STREAMED` and plugins declare `BodyChunked`:

```
Backend streams response
         │
         ▼
Envoy sends chunk₁ ──────────────────────────────────────────────────┐
         │                                                           │
    Server.Process()                                                 │
         ├── Accumulate: responseBody = append(responseBody, chunk₁) │
         ├── ChunkProcessor plugins: ProcessResponseChunk(chunk₁)    │
         ├── Send ack to Envoy (BodyResponse{}) ─────────────────────┤
         │                                          Envoy forwards   │
         │                                          chunk₁ to client │
         ▼                                                           │
Envoy sends chunk₂ ──────────────────────────────────────────────────┤
         │                                                           │
         ├── Accumulate: responseBody = append(responseBody, chunk₂) │
         ├── ChunkProcessor plugins: ProcessResponseChunk(chunk₂)    │
         ├── Send ack ───────────────────────────────────────────────┤
         │                                          → chunk₂ to client
         ▼                                                           │
Envoy sends chunk₃ (EndOfStream=true) ──────────────────────────────┘
         │
         ├── Accumulate final chunk
         ├── ChunkProcessor plugins: ProcessResponseChunk(chunk₃, isFinal=true)
         ├── Parse full accumulated body (JSON or SSE by Content-Type)
         ├── ResponseProcessor plugins: ProcessResponse(fullBody)
         └── Send final response to Envoy
```

Key properties:
- **Client gets real-time streaming** — each chunk is acked and forwarded immediately
- **ChunkProcessor plugins** see each chunk in real-time for lightweight per-chunk work
- **ResponseProcessor plugins** still get the full parsed body on the final chunk (backward compatible)
- **Memory accumulation** still happens (for the final ProcessResponse call) — this is a known trade-off documented in the code. A future enhancement could allow chunk-only plugins to opt out of accumulation entirely

### 6. Migration Path

**Existing plugins** that don't implement `BodyModeCapability`:
- Default to `{RequestBody: BodyFull, ResponseBody: BodyFull}`
- Framework logs: `WARN plugin "X" does not declare body requirements, assuming BUFFERED for both`
- No breaking changes — everything works exactly as before

**New plugins** should implement `BodyModeCapability`. The framework validates `ChunkProcessor` implementation at startup:
- Declares `BodyChunked` but doesn't implement `ChunkProcessor` → startup **ERROR**
- Implements `ChunkProcessor` but declares `BodyFull` → `ChunkProcessor` methods are never called (no harm, but logged as INFO)

### 7. Future: Dynamic Mode Override

Envoy ext_proc supports `mode_override` in responses, allowing the server to change the processing mode per-request. A future enhancement could use this:

1. During request processing, after the ProfilePicker selects a profile, check if the selected profile's response plugins need body access
2. If no response plugin needs the body → override `response_body_mode` to `NONE` for this request
3. This is a per-request optimization, not a global config change

This is explicitly **out of scope** for the initial implementation. It requires careful testing and is opt-in via configuration:

```yaml
framework:
  dynamicModeOverride: false  # default: validate only, don't override
```

### 8. Implementation Plan

| Phase | Scope | Files |
|-------|-------|-------|
| 1 | Add `BodyModeCapability` and `ChunkProcessor` interfaces | `pkg/framework/interface/requesthandling/plugins.go` |
| 2 | Add `BodyRequirements` types | `pkg/framework/interface/requesthandling/types.go` |
| 3 | Add validation logic at startup | `pkg/config/loader/configloader.go` |
| 4 | Add chunk ack + ChunkProcessor dispatch in server loop | `pkg/handlers/server.go` |
| 5 | Declare capabilities on existing plugins | Each plugin's `plugin.go` |
| 6 | Add startup validation logging | `pkg/handlers/server.go` or `cmd/main.go` |

### 9. Relation to PR #138

[PR #138](https://github.com/llm-d/llm-d-inference-payload-processor/pull/138) (SSE response parsing) addresses a subset of this proposal:
- Adds chunk acks for non-final response body chunks (phase 4 above)
- Adds SSE parsing for token extraction from streaming responses
- Does NOT add the capability declaration or validation — plugins don't declare their needs

This proposal builds on PR #138's streaming infrastructure and adds the framework-level intelligence to reason about plugin requirements.
