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
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/datalayer"
)

type testValue struct{ Value int }

func (t testValue) Clone() datalayer.Cloneable { return testValue{Value: t.Value} }

// TestGetOrCreate tests that a model is created with the correct name and non-nil Attributes.
func TestGetOrCreate(t *testing.T) {
	s := NewStore()

	m := s.GetOrCreate("llama-3")
	if m == nil {
		t.Fatal("expected non-nil model")
	}
	if m.Name != "llama-3" {
		t.Errorf("expected Name %q, got %q", "llama-3", m.Name)
	}
	if m.Attributes == nil {
		t.Fatal("expected non-nil Attributes")
	}
}

// TestGetOrCreateReturnsSameInstance tests that repeated calls return the same *Model.
func TestGetOrCreateReturnsSameInstance(t *testing.T) {
	s := NewStore()

	m1 := s.GetOrCreate("llama-3")
	m2 := s.GetOrCreate("llama-3")
	if m1 != m2 {
		t.Error("expected same *Model instance on repeated calls")
	}
}

// TestDelete tests that deleting a model and calling GetOrCreate returns a fresh empty model.
func TestDelete(t *testing.T) {
	s := NewStore()

	s.GetOrCreate("llama-3").Attributes.Put("key", testValue{Value: 42})
	s.Delete("llama-3")

	if _, ok := s.GetOrCreate("llama-3").Attributes.Get("key"); ok {
		t.Error("expected fresh Attributes after Delete + GetOrCreate")
	}
}

// TestDeleteNonExistent tests that deleting a missing model does not panic.
func TestDeleteNonExistent(t *testing.T) {
	s := NewStore()
	s.Delete("does-not-exist")
}

// TestModelsIsolated tests that different models have independent Attributes.
func TestModelsIsolated(t *testing.T) {
	s := NewStore()

	s.GetOrCreate("gpt-4").Attributes.Put("metric", testValue{Value: 1})
	s.GetOrCreate("llama-3").Attributes.Put("metric", testValue{Value: 2})

	v1, ok := s.GetOrCreate("gpt-4").Attributes.Get("metric")
	if !ok || v1.(testValue).Value != 1 {
		t.Errorf("expected 1 for gpt-4, got %v", v1)
	}

	v2, ok := s.GetOrCreate("llama-3").Attributes.Get("metric")
	if !ok || v2.(testValue).Value != 2 {
		t.Errorf("expected 2 for llama-3, got %v", v2)
	}
}

// TestAttributePersistence tests that attributes survive multiple GetOrCreate calls.
func TestAttributePersistence(t *testing.T) {
	s := NewStore()

	s.GetOrCreate("llama-3").Attributes.Put("running-requests", testValue{Value: 5})

	v, ok := s.GetOrCreate("llama-3").Attributes.Get("running-requests")
	if !ok {
		t.Fatal("expected attribute to persist across GetOrCreate calls")
	}
	if v.(testValue).Value != 5 {
		t.Errorf("expected 5, got %d", v.(testValue).Value)
	}
}

// TestIndependentStoreInstances tests that two Store instances are fully isolated.
func TestIndependentStoreInstances(t *testing.T) {
	s1 := NewStore()
	s2 := NewStore()

	s1.GetOrCreate("llama-3").Attributes.Put("key", testValue{Value: 1})

	if _, ok := s2.GetOrCreate("llama-3").Attributes.Get("key"); ok {
		t.Error("expected s2 to be independent from s1")
	}
}

// TestModels tests that Models() returns all tracked model names.
func TestModels(t *testing.T) {
	s := NewStore()
	s.GetOrCreate("gpt-4")
	s.GetOrCreate("llama-3")
	s.GetOrCreate("mistral")

	names := s.Models()
	if len(names) != 3 {
		t.Errorf("expected 3 models, got %d", len(names))
	}
}

// TestConcurrentAccess tests thread-safety of Store operations.
func TestConcurrentAccess(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup

	// All goroutines GetOrCreate the same name — must return same instance.
	models := make([]*datalayer.Model, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			models[idx] = s.GetOrCreate("same-model")
		}(i)
	}
	wg.Wait()

	first := models[0]
	for i := 1; i < 50; i++ {
		if models[i] != first {
			t.Errorf("goroutine %d got a different *Model instance", i)
		}
	}

	// Concurrent create and delete must not race.
	for i := 0; i < 50; i++ {
		wg.Add(2)
		name := string(rune('a' + i%10))
		go func(n string) {
			defer wg.Done()
			s.GetOrCreate(n)
		}(name)
		go func(n string) {
			defer wg.Done()
			s.Delete(n)
		}(name)
	}
	wg.Wait()
}
