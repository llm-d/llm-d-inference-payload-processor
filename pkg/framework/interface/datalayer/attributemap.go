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

import (
	"fmt"
	"sync"
)

// Cloneable types support cloning, required for all values stored in an
// AttributeMap to prevent unintended mutations.
type Cloneable interface {
	Clone() Cloneable
}

// StringAttribute is a Cloneable wrapper around a plain string.
type StringAttribute string

func (s StringAttribute) Clone() Cloneable {
	return s
}

// AttributeMap stores flexible, goroutine-safe metadata about a model.
// Stored values must be Cloneable.
type AttributeMap interface {
	// Put stores or updates an attribute. Empty keys and nil values are no-ops.
	Put(key string, value Cloneable)

	// Get returns a cloned copy of the attribute value, if found.
	Get(key string) (Cloneable, bool)

	Delete(key string)

	// Keys returns all attribute keys, in no particular order.
	Keys() []string

	Clone() AttributeMap
}

// Attributes is a goroutine-safe AttributeMap backed by sync.Map.
type Attributes struct {
	data sync.Map // key: attribute name, value: Cloneable
}

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

// ReadAttributeKey reads an attribute by key and asserts it to type T, which
// must match the concrete type returned by the stored value's Clone().
func ReadAttributeKey[T any](attrs AttributeMap, key string) (T, error) {
	var zero T

	raw, ok := attrs.Get(key)
	if !ok {
		return zero, fmt.Errorf("attribute %q: not found", key)
	}

	val, ok := raw.(T)
	if !ok {
		return zero, fmt.Errorf("unexpected type for key %q: got %T, want %T", key, raw, zero)
	}

	return val, nil
}
