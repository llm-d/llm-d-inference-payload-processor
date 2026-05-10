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
	"sync"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
)

// Datastore is the interface for reading and updating the model store.
type Datastore interface {
	GetOrCreateModel(name string) *datalayer.Model
	DeleteModel(name string)
	Models() []string
}

// store is a thread-safe registry of Model entries keyed by model name.
// The outer key is the model name; each Model holds an AttributeMap for
// dynamic runtime metrics (e.g. "running-requests", "pool-latency") and
// any static metadata added in future (e.g. vendor, family).
//
// All operations are thread-safe using RWMutex.
type store struct {
	mu     sync.RWMutex
	models map[string]*datalayer.Model
}

// NewStore creates and returns a new Datastore instance.
func NewStore() Datastore {
	return &store{models: make(map[string]*datalayer.Model)}
}

// GetOrCreateModel returns the Model for name, creating it atomically if it does not exist.
func (s *store) GetOrCreateModel(name string) *datalayer.Model {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.models[name]; ok {
		return m
	}
	m := datalayer.NewModel(name)
	s.models[name] = m
	return m
}

// DeleteModel removes a model by name. No-op if it does not exist.
func (s *store) DeleteModel(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.models, name)
}

// Models returns the names of all tracked models. Order is not guaranteed.
func (s *store) Models() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.models))
	for n := range s.models {
		names = append(names, n)
	}
	return names
}
