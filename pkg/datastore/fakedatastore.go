package datastore

import (
	"sync"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
)

// fakeDataStore is an in-memory DataStore for tests.
type fakeDataStore struct {
	mu     sync.Mutex
	models map[string]datalayer.Model
}

func NewFakeDataStore() *fakeDataStore {
	return &fakeDataStore{models: make(map[string]datalayer.Model)}
}

func (f *fakeDataStore) GetOrCreateModel(name string) datalayer.Model {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.models[name]; ok {
		return m
	}
	m := datalayer.NewModel(name)
	f.models[name] = m
	return m
}

func (f *fakeDataStore) DeleteModel(name string) {}

func (f *fakeDataStore) Models() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.models))
	for name := range f.models {
		names = append(names, name)
	}
	return names
}

func (f *fakeDataStore) GetModels(predicate func(datalayer.Model) bool) []datalayer.Model {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]datalayer.Model, 0, len(f.models))
	for _, m := range f.models {
		if predicate(m) {
			result = append(result, m)
		}
	}
	return result
}
