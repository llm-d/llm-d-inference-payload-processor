/*
Copyright 2026 The llm-d Authors.

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

package datalayer

import "sync"

// Cloneable types support cloning of the value.
// All values stored in AttributeMap must implement this interface
// to ensure data isolation and prevent unintended mutations.
type Cloneable interface {
	Clone() Cloneable
}

// AttributeMap is used to store flexible metadata or traits
// across different aspects of a model.
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
func NewAttributes() AttributeMap {
	return &Attributes{}
}

func (a *Attributes) Put(key string, value Cloneable) {
	if key == "" {
		return
	}
	if value == nil {
		return
	}
	a.data.Store(key, value)
}

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

func (a *Attributes) Delete(key string) {
	a.data.Delete(key)
}

func (a *Attributes) Keys() []string {
	keys := []string{}
	a.data.Range(func(key, value any) bool {
		if k, ok := key.(string); ok {
			keys = append(keys, k)
		}
		return true
	})
	return keys
}

func (a *Attributes) Clone() AttributeMap {
	clone := NewAttributes()
	a.data.Range(func(key, value any) bool {
		if k, ok := key.(string); ok {
			if v, ok := value.(Cloneable); ok {
				clone.Put(k, v)
			}
		}
		return true
	})
	return clone
}
