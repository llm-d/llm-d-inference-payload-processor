/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package datastore

import "sync"

// Cloneable types support cloning of the value.
// All values stored in AttributeMap must implement this interface
// to ensure data isolation and prevent unintended mutations.
type Cloneable interface {
	Clone() Cloneable
}

// AttributeMap is used to store flexible metadata or traits
// across different aspects of an inference server.
// Stored values must be Cloneable.
//
// All operations are goroutine-safe.
type AttributeMap interface {
	// Put stores or updates an attribute.
	// Empty keys and nil values are ignored (no-op).
	Put(key string, value Cloneable)

	// Get retrieves a cloned copy of the attribute value.
	// Returns (value, true) if found, (nil, false) if not found.
	// The returned value is a clone to prevent unintended mutations.
	Get(key string) (Cloneable, bool)

	// Delete removes an attribute by key.
	// No-op if key doesn't exist.
	Delete(key string)

	// Keys returns all attribute keys as a string slice.
	// Order is not guaranteed.
	Keys() []string

	// Clone creates a deep copy of the entire attribute map.
	Clone() AttributeMap
}

// Attributes provides a goroutine-safe implementation of AttributeMap.
// Uses sync.Map for concurrent access without explicit locking.
type Attributes struct {
	data sync.Map // key: attribute name (string), value: attribute value (Cloneable)
}

// NewAttributes creates a new AttributeMap instance.
//
// Algorithm:
// 1. Create new Attributes instance with zero-value sync.Map
// 2. Return pointer to Attributes as AttributeMap interface
func NewAttributes() AttributeMap {
	return &Attributes{}
}

// Put stores or updates an attribute.
//
// Algorithm:
// 1. If key is empty, return without storing (no-op)
// 2. Check if value is nil
// 3. If nil, return without storing (no-op)
// 4. If non-nil, store key-value pair in sync.Map using Store()
func (a *Attributes) Put(key string, value Cloneable) {
	if key == "" {
		return
	}
	if value == nil {
		return
	}
	a.data.Store(key, value)
}

// Get retrieves a cloned copy of the attribute value.
//
// Algorithm:
// 1. Load value from sync.Map by key using Load()
// 2. If key not found, return (nil, false)
// 3. If found, type assert value to Cloneable interface
// 4. If type assertion fails, return (nil, false)
// 5. Call Clone() on the value to create independent copy
// 6. Return (cloned value, true)
func (a *Attributes) Get(key string) (Cloneable, bool) {
	value, ok := a.data.Load(key)
	if !ok {
		return nil, false
	}
	cloneable, ok := value.(Cloneable)
	if !ok {
		return nil, false
	}
	return cloneable.Clone(), true
}

// Delete removes an attribute by key.
//
// Algorithm:
// 1. Call Delete() on sync.Map with the provided key
// 2. No return value (no-op if key doesn't exist)
func (a *Attributes) Delete(key string) {
	a.data.Delete(key)
}

// Keys returns all attribute keys as a string slice.
//
// Algorithm:
// 1. Initialize empty string slice for keys
// 2. Call Range() on sync.Map to iterate all entries
// 3. For each entry, type assert key to string
// 4. If assertion succeeds, append key to slice
// 5. Continue iteration (return true from Range callback)
// 6. Return collected keys slice
func (a *Attributes) Keys() []string {
	keys := []string{}
	a.data.Range(func(key, value interface{}) bool {
		if k, ok := key.(string); ok {
			keys = append(keys, k)
		}
		return true
	})
	return keys
}

// Clone creates a deep copy of the entire attribute map.
//
// Algorithm:
// 1. Create new AttributeMap using NewAttributes()
// 2. Call Range() on sync.Map to iterate all entries
// 3. For each entry:
//    - Type assert key to string
//    - Type assert value to Cloneable
//    - If both assertions succeed, call Put() on new map with key and value
//    - Put() stores the original value; cloning happens on Get()
// 4. Continue iteration (return true from Range callback)
// 5. Return new AttributeMap with cloned contents
func (a *Attributes) Clone() AttributeMap {
	clone := NewAttributes()
	a.data.Range(func(key, value interface{}) bool {
		if k, ok := key.(string); ok {
			if v, ok := value.(Cloneable); ok {
				clone.Put(k, v)
			}
		}
		return true
	})
	return clone
}
