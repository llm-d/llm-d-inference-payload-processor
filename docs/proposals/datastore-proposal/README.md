# Proposal: In-Memory Datastore for the Inference Payload Processor

## Summary

Introduce a thread-safe, in-memory datastore package (`pkg/framework/datastore`) that provides shared, persistent-across-requests state for IPP plugins. The datastore is the data layer required by latency-based routing and any future feature that needs to track state across multiple request-response cycles.

## Motivation

### Background

The IPP plugin pipeline (Filter → Scorer → Picker) processes each request independently. Latency-based routing requires scoring inference pool candidates based on observed metrics — in-flight request counts, KV-cache hit potential, TTFT, and end-to-end latency. These metrics are computed from data collected across many requests, not from a single request in isolation.

The existing `CycleState` mechanism provides per-request plugin communication but is cleared after each request. There is no mechanism today for plugins to share or accumulate state across requests.

### Goals

- Provide a shared, in-memory storage layer accessible to all IPP plugins via the `Handle` interface.
- Support multiple independent data topics (e.g., `"model-latency"`, `"request-content-prefix"`) so that different data collectors do not interfere with each other.
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

### Datastores

```go
type Datastores struct {
    mu      sync.RWMutex
    keyName map[string]AttributeMap
}

func NewDatastores() *Datastores
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

## Appendixes

### Appendix A: Implementation Algorithms

This section provides detailed algorithms for all datastore operations.

#### NewDatastores Algorithm

1. Create new `Datastores` instance with empty `keyName` map
2. Return pointer to the new `Datastores` instance

#### GetOrCreateStore Algorithm

1. Validate datastoreKey is non-empty, return `ErrEmptyDatastoreKey` if empty
2. Thread-safely (read lock) check if store exists in `keyName` for datastoreKey
3. If exists, return existing AttributeMap
4. If not exists, thread-safely (write lock) create new AttributeMap using `NewAttributes()` and store in map
5. Double-check after acquiring write lock in case another goroutine created it
6. Return newly created or existing AttributeMap

#### DeleteStore Algorithm

1. Validate datastoreKey is non-empty, return `ErrEmptyDatastoreKey` if empty
2. Thread-safely (write lock) remove datastoreKey store from `keyName` map
3. Return nil (no-op if key doesn't exist)

#### NewAttributes Algorithm

1. Create new `Attributes` instance with zero-value sync.Map
2. Return pointer to `Attributes` as `AttributeMap` interface

#### Put Algorithm

1. If key is empty, return without storing (no-op)
2. Check if value is nil
3. If nil, return without storing (no-op)
4. If non-nil, store key-value pair in sync.Map using `Store()`

#### Get Algorithm

1. Load value from sync.Map by key using `Load()`
2. If key not found, return (nil, false)
3. If found, type assert value to Cloneable interface
4. If type assertion fails, return (nil, false)
5. Call `Clone()` on the value to create independent copy
6. Return (cloned value, true)

#### Delete Algorithm

1. Call `Delete()` on sync.Map with the provided key
2. No return value (no-op if key doesn't exist)

#### Keys Algorithm

1. Initialize empty string slice for keys
2. Call `Range()` on sync.Map to iterate all entries
3. For each entry, type assert key to string
4. If assertion succeeds, append key to slice
5. Continue iteration (return true from Range callback)
6. Return collected keys slice

#### Clone Algorithm

1. Create new AttributeMap using `NewAttributes()`
2. Call `Range()` on sync.Map to iterate all entries
3. For each entry:
   - Type assert key to string
   - Type assert value to Cloneable
   - If both assertions succeed, call `Put()` on new map with key and value
   - `Put()` stores the original value; cloning happens on `Get()`
4. Continue iteration (return true from Range callback)
5. Return new AttributeMap with cloned contents

### Appendix B: Unit Test Specifications

#### Datastores Tests (`datastore_test.go`)

| Scenario | Input | Expected |
|----------|-------|----------|
| Create new store | datastoreKey="test-store" | Returns new AttributeMap, no error |
| Get existing store | datastoreKey="test-store" (already exists) | Returns same AttributeMap instance, no error |
| Empty datastoreKey | datastoreKey="" | Returns nil, ErrEmptyDatastoreKey |
| Delete existing store | datastoreKey="test-store" (exists) | Store removed from registry, no error |
| Delete non-existent store | datastoreKey="non-existent" | No-op, no error |
| Empty key on delete | datastoreKey="" | Returns ErrEmptyDatastoreKey |
| Multiple stores isolated | Create "store-1" and "store-2", put different data | Each store maintains independent data |
| Concurrent GetOrCreateStore | 100 goroutines call GetOrCreateStore("same-key") | All goroutines get same AttributeMap instance |
| Concurrent operations | 50 goroutines GetOrCreateStore, 50 goroutines DeleteStore | No panics, no deadlocks, thread-safe |
| NewDatastores resets | Create store, call NewDatastores() | Previous stores cleared, fresh registry |
| Data persistence | Create store, put data, get store again | Data persists across GetOrCreateStore calls |

#### AttributeMap Tests (`attributemap_test.go`)

| Scenario | Input | Expected |
|----------|-------|----------|
| Put and Get | key="test", value=TestValue{42} | Get returns cloned value, ok=true |
| Get non-existent | key="missing" | Returns nil, ok=false |
| Put empty key | key="", value=TestValue{42} | No-op, Keys() returns empty |
| Put nil value | key="test", value=nil | No-op, Get returns nil, ok=false |
| Delete existing | Put key, then Delete key | Get returns nil, ok=false |
| Delete non-existent | Delete key that doesn't exist | No-op, no panic |
| Keys on empty map | New AttributeMap | Returns empty slice |
| Keys with data | Put 3 keys | Returns slice with 3 keys |
| Clone empty map | New AttributeMap, Clone() | Returns new empty AttributeMap |
| Clone with data | Put 2 keys, Clone() | Clone has same keys/values, independent |
| Clone independence | Clone map, modify clone | Original unchanged |
| Get returns clone | Put value, Get twice | Two independent clones returned |
| Update existing key | Put key="test" twice with different values | Second value overwrites first |
| Multiple keys | Put 10 different keys | All keys retrievable, Keys() returns 10 |
| Concurrent Put/Get/Delete | 100 goroutines doing Put, 100 doing Get, 100 doing Delete | No panics, no race conditions |

**Test Rules:**
- All tests use `testCloneableValue` struct implementing Cloneable
- No external dependencies to mock (datastore has no external dependencies)
- Every field in Expected column must be asserted
- Concurrent tests verify thread safety with race detector


### Appendix C: Dependencies

- `sync` — for RWMutex (Datastores) and sync.Map (AttributeMap) for thread-safe operations
- `errors` — for ErrEmptyDatastoreKey error variable
- `github.com/llm-d/llm-d-inference-payload-processor/pkg/framework` — for Handle interface integration
