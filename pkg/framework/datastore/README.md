# Inference Payload Processor Data Store

## Package
`pkg/framework/datastore`

## Purpose

Provides a thread-safe, in-memory data storage layer that manages data collected by multiple data collection plugins.
The datastore focuses solely on storing and retrieving data entries by name. All other fields are provided by the data collection plugins.


## Related Specifications

- Used by [Handle](../handle.go) - provides plugin access to datastores via `GetAttributeMap()`

## Non-Goals

- Persistent storage (disk, database) — datastore is in-memory only
- TTL or expiration — entries remain until explicitly deleted
- Search or filtering — only direct lookup by key
- Query language — simple Put/Get/Delete operations only
- Distributed storage — each IPP pod has its own datastore instance
- Authentication or authorization — caller (plugins) are trusted
- Schema validation — values are Cloneable interface, no type enforcement
- Automatic cleanup — plugins manage their own data lifecycle
- Metrics collection — plugins are responsible for collecting and storing metrics

## Core Components

Each component is managed in a separate go file in the package.

### Datastores (`datastore.go`)

Manages multiple named data stores, each identified by a datastoreKey string. Provides the top-level registry for all topic-specific AttributeMaps.

```go
// Global package-level variable to hold all collector data stores
var Data *Datastores

type Datastores struct {
    mu      sync.RWMutex
    keyName map[string]AttributeMap
}

func NewDatastores()
func (ds *Datastores) GetOrCreateStore(datastoreKey string) (AttributeMap, error)
func (ds *Datastores) DeleteStore(datastoreKey string) error
```

**Behavior:**
- All operations are thread-safe using RWMutex
- `GetOrCreateStore()` returns existing AttributeMap or creates new one atomically
- `DeleteStore()` removes entire datastoreKey store and all its contents
- Empty datastoreKey strings are rejected with `ErrEmptyDatastoreKey`
- Double-check locking pattern prevents race conditions during store creation

**NewDatastores Algorithm:**
1. Instantiate package-level variable `Data` to new `Datastores` instance, and empty `keyName` map
2. No return value

**GetOrCreateStore Algorithm:**
1. Validate datastoreKey is non-empty, return `ErrEmptyDatastoreKey` if empty
2. Thread-safely (read lock) check if store exists in `keyName` for datastoreKey
3. If exists, return existing AttributeMap
4. If not exists, thread-safely (write lock) create new AttributeMap using `NewAttributes()` and store in map
5. Double-check after acquiring write lock in case another goroutine created it
6. Return newly created or existing AttributeMap

**DeleteStore Algorithm:**
1. Validate datastoreKey is non-empty, return `ErrEmptyDatastoreKey` if empty
2. Thread-safely (write lock) remove datastoreKey store from `keyName` map
3. Return nil (no-op if key doesn't exist)

### Cloneable Interface (`attributemap.go`)

Defines the contract for types that can create deep copies of themselves. Required for all values stored in AttributeMap to ensure data isolation.

```go
// Cloneable types support cloning of the value.
type Cloneable interface {
    Clone() Cloneable
}
```

**Purpose:**
- Ensures data isolation by requiring all stored values to be cloneable
- Prevents unintended mutations of shared data across goroutines
- Plugins must implement Cloneable for their custom types

**Implementation Requirements:**
- `Clone()` must return a deep copy of the value
- The returned copy must be independent of the original
- Modifications to the clone must not affect the original
- All nested structures (slices, maps, pointers) must be deep-copied

```go
type RequestMetrics struct {
    TotalRequests   int64
    RequestsByModel map[string]int64
}

```

### AttributeMap Interface (`attributemap.go`)

Provides a flexible, goroutine-safe key-value storage for metadata and traits. Each AttributeMap represents a topic-specific datastore (e.g., "request-content-prefix", "inference-pool-latency").

```go
// AttributeMap is used to store flexible metadata or traits
// across different aspects of an inference server.
// Stored values must be Cloneable.
type AttributeMap interface {
    Put(string, Cloneable)
    Get(string) (Cloneable, bool)
    Delete(string)
    Keys() []string
    Clone() AttributeMap
}

// Attributes provides a goroutine-safe implementation of AttributeMap.
type Attributes struct {
    data sync.Map // key: attribute name (string), value: attribute value (Cloneable)
}

func NewAttributes() AttributeMap
func (a *Attributes) Put(key string, value Cloneable)
func (a *Attributes) Get(key string) (Cloneable, bool)
func (a *Attributes) Delete(key string)
func (a *Attributes) Keys() []string
func (a *Attributes) Clone() AttributeMap
```

**AttributeMap Interface Methods:**
- `Put(key, value)` — stores or updates an attribute (nil values and empty keys are ignored)
- `Get(key)` — retrieves a cloned copy of the attribute value, returns (value, true) if found or (nil, false) if not found
- `Delete(key)` — removes an attribute by key (no-op if key doesn't exist)
- `Keys()` — returns all attribute keys as a string slice (order not guaranteed)
- `Clone()` — creates a deep copy of the entire attribute map

**Attributes Implementation Details:**
- Uses `sync.Map` for goroutine-safe concurrent access without explicit locking
- All operations are thread-safe by design of sync.Map
- No separate mutex needed due to sync.Map's built-in concurrency safety
- `Get()` returns cloned values to prevent unintended mutations

**NewAttributes Algorithm:**
1. Create new `Attributes` instance with zero-value sync.Map
2. Return pointer to `Attributes` as `AttributeMap` interface

**Put Algorithm:**
1. If key is empty, return without storing (no-op)
2. Check if value is nil
3. If nil, return without storing (no-op)
4. If non-nil, store key-value pair in sync.Map using `Store()`

**Get Algorithm:**
1. Load value from sync.Map by key using `Load()`
2. If key not found, return (nil, false)
3. If found, type assert value to Cloneable interface
4. If type assertion fails, return (nil, false)
5. Call `Clone()` on the value to create independent copy
6. Return (cloned value, true)

**Delete Algorithm:**
1. Call `Delete()` on sync.Map with the provided key
2. No return value (no-op if key doesn't exist)

**Keys Algorithm:**
1. Initialize empty string slice for keys
2. Call `Range()` on sync.Map to iterate all entries
3. For each entry, type assert key to string
4. If assertion succeeds, append key to slice
5. Continue iteration (return true from Range callback)
6. Return collected keys slice

**Clone Algorithm:**
1. Create new AttributeMap using `NewAttributes()`
2. Call `Range()` on sync.Map to iterate all entries
3. For each entry:
   - Type assert key to string
   - Type assert value to Cloneable
   - If both assertions succeed, call `Put()` on new map with key and value
   - `Put()` stores the original value; cloning happens on `Get()`
4. Continue iteration (return true from Range callback)
5. Return new AttributeMap with cloned contents

### Package Level Error Variables

```go
var (
    ErrEmptyDatastoreKey = errors.New("datastore key cannot be empty")
)
```

**Error Handling:**
- `ErrEmptyDatastoreKey` — returned when datastoreKey is empty in `GetOrCreateStore` or `DeleteStore`
- AttributeMap methods do not return errors; `Get()` returns (nil, false) for missing keys
- Type assertion failures in `Get()` return (nil, false)

## Configuration

The datastore is initialized by calling `NewDatastores()` at server startup.
No YAML configuration is required.


## Unit Tests

### Datastores Tests (`datastore_test.go`)

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

### AttributeMap Tests (`attributemap_test.go`)

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

**Rules:**
- All tests use `testCloneableValue` struct implementing Cloneable
- Mock no external components (datastore is self-contained)
- Every field in Expected column must be asserted
- Concurrent tests verify thread safety with race detector

## Dependencies

- `sync` — for RWMutex (Datastores) and sync.Map (AttributeMap) for thread-safe operations
- `errors` — for ErrEmptyDatastoreKey error variable
- `github.com/llm-d/llm-d-inference-payload-processor/pkg/framework` — for Handle interface integration
