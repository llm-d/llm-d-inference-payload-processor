# Session Affinity Scorer

A model-selector scorer plugin that biases model selection toward the model previously selected for a given session. It tracks session-to-model mappings in an LRU cache with TTL, improving KV cache hit rates for multi-turn conversations.

## How It Works

- **No session ID in request:** Score 0.0 for all models (no opinion).
- **Session ID present, first turn** (no prior model recorded): Score 0.0 for all (no opinion) — other scorers decide freely.
- **Session ID present, follow-up turn** (prior model known and still a candidate): Score 1.0 for the prior model, 0.0 for all others.
- **Prior model no longer in candidates** (scaled down / removed): Score 0.0 for all (no opinion) — falls back to other scorers.

The plugin tracks session-to-model mappings in an LRU cache with configurable TTL via the `ResponseProcessor` interface. After each request, it records which model was selected for which session ID. This state is best-effort — it is lost on pod restart.

The session ID is always echoed back as a response header using the configured header key.

### Optimistic Session ID

When a request arrives with **no session ID at all**, the plugin optimistically generates a UUID and echoes it in the response header (e.g. `x-session-id`). If the client echoes this ID back on subsequent requests, session affinity is established automatically — no client-side configuration required.

## Session ID Lookup

The plugin reads the session ID from a single configured request header key. Default key: `x-session-id`.

## Configuration

```yaml
plugins:
- name: session-affinity
  type: session-affinity
  parameters:
    sessionIdKey: x-session-id  # optional, default: "x-session-id"
    maxSessions: 10000          # optional, default: 10000 (max LRU cache entries)
    ttlSeconds: 3600            # optional, default: 3600 (1 hour)
```

## Profile Wiring

The plugin must appear in both the `request` section (as a scorer with a weight) and the `response` section (to record the selected model for future requests):

```yaml
profiles:
- name: default
  plugins:
    request:
    - pluginRef: model-selector
    - pluginRef: session-affinity
      weight: 1.0
    response:
    - pluginRef: session-affinity
```

## Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `sessionIdKey` | string | `"x-session-id"` | Request header key to read the session ID from |
| `maxSessions` | int | `10000` | Maximum number of session-to-model mappings in the LRU cache |
| `ttlSeconds` | int | `3600` | Time-to-live in seconds for each cache entry |

## Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `ipp_session_affinity_cache_hits_total` | Counter | Session found in cache (follow-up turn) |
| `ipp_session_affinity_cache_misses_total` | Counter | Session not found in cache (first turn) |
| `ipp_session_affinity_cache_size` | Gauge | Current number of entries in the cache |
| `ipp_session_affinity_cache_evictions_total` | Counter | Entries evicted (LRU or TTL expiry) |
