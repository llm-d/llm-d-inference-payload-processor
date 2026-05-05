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

package datastore

import (
	"errors"
	"sync"
)

// Global package-level variable to hold all collector data stores.
// Initialized by calling NewDatastores() at server startup.
var Data *Datastores

// ErrEmptyDatastoreKey is returned when a datastoreKey is empty.
var ErrEmptyDatastoreKey = errors.New("datastore key cannot be empty")

// Datastores manages multiple named data stores, each identified by a datastoreKey string.
// Provides the top-level registry for all topic-specific AttributeMaps.
//
// All operations are thread-safe using RWMutex.
type Datastores struct {
	mu      sync.RWMutex
	keyName map[string]AttributeMap
}

// NewDatastores initializes the global Data variable with a new Datastores instance.
func NewDatastores() {
	Data = &Datastores{
		keyName: make(map[string]AttributeMap),
	}
}

// GetOrCreateStore returns an existing AttributeMap or creates a new one atomically.
// Returns ErrEmptyDatastoreKey if datastoreKey is empty.
func (ds *Datastores) GetOrCreateStore(datastoreKey string) (AttributeMap, error) {
	if datastoreKey == "" {
		return nil, ErrEmptyDatastoreKey
	}

	// Fast path: check if store exists with read lock
	ds.mu.RLock()
	store, ok := ds.keyName[datastoreKey]
	ds.mu.RUnlock()
	if ok {
		return store, nil
	}

	// Slow path: create new store with write lock
	ds.mu.Lock()
	defer ds.mu.Unlock()

	// Double-check in case another goroutine created it
	store, ok = ds.keyName[datastoreKey]
	if ok {
		return store, nil
	}

	store = NewAttributes()
	ds.keyName[datastoreKey] = store
	return store, nil
}

// DeleteStore removes a datastore by key.
// Returns ErrEmptyDatastoreKey if datastoreKey is empty.
// No-op if the key doesn't exist.
func (ds *Datastores) DeleteStore(datastoreKey string) error {
	if datastoreKey == "" {
		return ErrEmptyDatastoreKey
	}

	ds.mu.Lock()
	defer ds.mu.Unlock()

	delete(ds.keyName, datastoreKey)
	return nil
}
