# Proposal: In-Memory Datastore for the Inference Payload Processor

## Summary

Introduce a thread-safe, in-memory datastore package (`pkg/framework/datastore`) that provides shared, persistent-across-requests state for IPP plugins. The datastore is the data layer required by latency-based routing and any future feature that needs to track state across multiple request-response cycles.

## Motivation

### Background

The IPP plugin pipeline (Filter → Scorer → Picker) processes each request independently. Latency-based routing requires scoring inference pool candidates based on observed metrics — in-flight request counts, KV-cache hit potential, TTFT, and end-to-end latency. These metrics are computed from data collected across many requests, not from a single request in isolation.

The existing `CycleState` mechanism provides per-request plugin communication but is cleared after each request. There is no mechanism today for plugins to share or accumulate state across requests.

### Goals

- Provide a shared, in-memory storage layer accessible to all IPP plugins via the `Handle` interface.
- Support multiple independent data topics (e.g., `"inference-pool-latency"`, `"request-content-prefix"`) so that different data collectors do not interfere with each other.
- Guarantee thread safety: concurrent reads and writes from different goroutines must not require external synchronization by callers.
- Ensure data isolation: retrieving a value must return an independent copy, so callers cannot accidentally mutate shared state.
- Keep the data layer general-purpose so it can serve future features beyond latency routing.

### Non-Goals

- Persistent storage (disk, database) — the datastore is in-memory only; data is lost on pod restart.
- TTL or automatic expiration — entries remain until a data collector explicitly deletes them.
- Distributed or shared storage across IPP replicas — each pod maintains its own independent datastore instance.
- Search, filtering, or query language — only direct lookup by key.
- Authentication or authorization — callers (plugins) are trusted.
- Schema validation — values must implement `Cloneable` but no further type enforcement is applied.

## Proposal

Implement a two-level key-value store:

1. **`Datastores`** — a global registry that maps topic names (`string`) to independent `AttributeMap` instances. Plugins acquire a topic store by calling `GetOrCreateStore(topic)`.
2. **`AttributeMap`** — a goroutine-safe key-value map for one topic. Values must implement the `Cloneable` interface; `Get()` always returns a clone to enforce data isolation.

The `Datastores` instance is initialized once at server startup via `NewDatastores()` and is accessible to all plugins through `Handle.GetAttributeMap()`.

## Design Details

### Cloneable Interface

All values stored in an `AttributeMap` must implement `Cloneable`:

```go
type Cloneable interface {
    Clone() Cloneable
}
```

This contract ensures that `Get()` can return an independent copy, preventing callers from mutating the stored value. Plugin-defined types (e.g., `PoolLatencyMetrics`, `PrefixMapping`) are responsible for implementing deep copies of any nested slices, maps, or pointers.

### AttributeMap

```go
type AttributeMap interface {
    Put(key string, value Cloneable)
    Get(key string) (Cloneable, bool)
    Delete(key string)
    Keys() []string
    Clone() AttributeMap
}
```

The concrete implementation (`Attributes`) uses `sync.Map` internally, providing safe concurrent access without requiring callers to hold a lock. `Get()` calls `Clone()` on the stored value before returning it, so mutations to the returned value never affect the datastore.

Empty keys and nil values are silently ignored on `Put()`.

### Datastores

```go
var Data *Datastores

type Datastores struct {
    mu      sync.RWMutex
    keyName map[string]AttributeMap
}

func NewDatastores()
func (ds *Datastores) GetOrCreateStore(datastoreKey string) (AttributeMap, error)
func (ds *Datastores) DeleteStore(datastoreKey string) error
```

`GetOrCreateStore` uses a read-then-write double-check locking pattern: it first acquires a read lock to check for an existing store (the common case), and only acquires a write lock when creation is needed. This avoids write-lock contention on the hot path.

`ErrEmptyDatastoreKey` is returned for empty topic keys on both `GetOrCreateStore` and `DeleteStore`.

### Integration with Handle

Plugins access the datastore through the `Handle` interface:

```go
store, err := handle.GetAttributeMap("inference-pool-latency")
```

This keeps the global `Data` variable hidden from plugins and makes the access point mockable in tests.

### Relationship to CycleState

| | Datastore | CycleState |
|---|---|---|
| **Scope** | Global, across requests | Single request-response cycle |
| **Lifetime** | Persistent until deleted | Cleared after each request |
| **Thread safety** | `sync.Map` + `RWMutex` | `sync.Map` |
| **Data isolation** | `Cloneable` — clone on read | Direct storage |
| **Access** | `Handle.GetAttributeMap()` | Direct parameter |
| **Use case** | Metrics, latency tracking, shared config | Plugin-to-plugin communication within a request |

### Intended Data Topics (Latency Routing)

The initial consumers of the datastore are the latency routing components described in the latency-based routing design:

- **`"inference-pool-latency"`** — maintained by the request-response tracker; stores per-pool moving averages of in-flight requests, TTFT, and end-to-end latency.
- **`"request-content-prefix"`** — maintained by the request-response tracker; maps request prompt prefix hashes to the inference pool that processed them, used to estimate KV-cache hit potential.

The datastore itself has no knowledge of these topics; they are defined and managed entirely by the data collectors that use it.
