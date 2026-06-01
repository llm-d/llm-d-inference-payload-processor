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

package inmemory

import (
	"fmt"
	"sync"
	"testing"

	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/datalayer"
)

type testValue struct{ Value int }

func (t testValue) Clone() datalayer.Cloneable { return testValue{Value: t.Value} }

// TestGetOrCreateModel tests creation, same-instance return, and attribute persistence.
func TestGetOrCreateModel(t *testing.T) {
	s := NewDatastore()

	m := s.GetOrCreateModel("llama-3")
	if m == nil || m.GetName() != "llama-3" || m.GetAttributes() == nil {
		t.Fatal("expected valid model with correct name and non-nil Attributes")
	}
	m.GetAttributes().Put("key", testValue{Value: 42})

	m2 := s.GetOrCreateModel("llama-3")
	if m != m2 {
		t.Error("expected same *Model instance on repeated calls")
	}
	if v, ok := m2.GetAttributes().Get("key"); !ok || v.(testValue).Value != 42 {
		t.Error("expected attribute to persist across GetOrCreateModel calls")
	}
}

// TestDeleteModel tests that delete+recreate yields a fresh model and that deleting a missing key is a no-op.
func TestDeleteModel(t *testing.T) {
	s := NewDatastore()

	s.GetOrCreateModel("llama-3").GetAttributes().Put("key", testValue{Value: 1})
	s.DeleteModel("llama-3")
	if _, ok := s.GetOrCreateModel("llama-3").GetAttributes().Get("key"); ok {
		t.Error("expected fresh Attributes after DeleteModel + GetOrCreateModel")
	}

	s.DeleteModel("does-not-exist") // must not panic
}

// TestModelsIsolated tests that different models have independent Attributes.
func TestModelsIsolated(t *testing.T) {
	s := NewDatastore()

	s.GetOrCreateModel("gpt-4").GetAttributes().Put("metric", testValue{Value: 1})
	s.GetOrCreateModel("llama-3").GetAttributes().Put("metric", testValue{Value: 2})

	v1, _ := s.GetOrCreateModel("gpt-4").GetAttributes().Get("metric")
	v2, _ := s.GetOrCreateModel("llama-3").GetAttributes().Get("metric")
	if v1.(testValue).Value != 1 || v2.(testValue).Value != 2 {
		t.Errorf("expected isolated attributes, got gpt-4=%v llama-3=%v", v1, v2)
	}
}

// TestIndependentStoreInstances tests that two Store instances are fully isolated.
func TestIndependentStoreInstances(t *testing.T) {
	s1, s2 := NewDatastore(), NewDatastore()
	s1.GetOrCreateModel("llama-3").GetAttributes().Put("key", testValue{Value: 1})
	if _, ok := s2.GetOrCreateModel("llama-3").GetAttributes().Get("key"); ok {
		t.Error("expected s2 to be independent from s1")
	}
}

// TestModels tests that Models() returns all tracked model names with correct content.
func TestModels(t *testing.T) {
	s := NewDatastore()
	s.GetOrCreateModel("gpt-4")
	s.GetOrCreateModel("llama-3")
	s.GetOrCreateModel("mistral")

	models := s.Models()
	if len(models) != 3 {
		t.Errorf("expected 3 models, got %d", len(models))
	}

	expected := map[string]bool{"gpt-4": true, "llama-3": true, "mistral": true}
	for _, name := range models {
		if !expected[name] {
			t.Errorf("unexpected model name: %s", name)
		}
		delete(expected, name)
	}
	if len(expected) > 0 {
		t.Errorf("missing expected models: %v", expected)
	}
}

// TestConcurrentAccess tests thread-safety of concurrent GetOrCreateModel calls.
func TestConcurrentAccess(t *testing.T) {
	s := NewDatastore()
	var wg sync.WaitGroup

	models := make([]datalayer.Model, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			models[idx] = s.GetOrCreateModel("same-model")
		}(i)
	}
	wg.Wait()

	for i := 1; i < 50; i++ {
		if models[i] != models[0] {
			t.Errorf("goroutine %d got a different *Model instance", i)
		}
	}
}

// TestAttributeNilValue tests that nil values are ignored (no-op) as per AttributeMap contract.
func TestAttributeNilValue(t *testing.T) {
	s := NewDatastore()
	m := s.GetOrCreateModel("test-model")
	attrs := m.GetAttributes()

	// Attempt to store nil value (should be ignored)
	attrs.Put("nil-key", nil)

	// Verify key does not exist (nil values are not stored)
	_, ok := attrs.Get("nil-key")
	if ok {
		t.Error("expected key to not exist when nil value is provided (nil values are ignored)")
	}

	// Verify that storing a valid value works
	attrs.Put("valid-key", testValue{Value: 42})
	val, ok := attrs.Get("valid-key")
	if !ok {
		t.Error("expected valid-key to exist")
	}
	if val.(testValue).Value != 42 {
		t.Errorf("expected value 42, got %v", val)
	}
}

// TestGetModelsAll tests that GetModels with an always-true predicate returns all models.
func TestGetModelsAll(t *testing.T) {
	s := NewDatastore()
	s.GetOrCreateModel("gpt-4")
	s.GetOrCreateModel("llama-3")
	s.GetOrCreateModel("mistral")

	models := s.GetModels(func(datalayer.Model) bool { return true })
	if len(models) != 3 {
		t.Errorf("expected 3 models, got %d", len(models))
	}

	names := make(map[string]bool)
	for _, m := range models {
		names[m.GetName()] = true
	}
	for _, expected := range []string{"gpt-4", "llama-3", "mistral"} {
		if !names[expected] {
			t.Errorf("missing expected model: %s", expected)
		}
	}
}

// TestGetModelsWithPredicate tests that GetModels filters correctly.
func TestGetModelsWithPredicate(t *testing.T) {
	s := NewDatastore()
	s.GetOrCreateModel("gpt-4").GetAttributes().Put("vendor", testValue{Value: 1})
	s.GetOrCreateModel("llama-3").GetAttributes().Put("vendor", testValue{Value: 2})
	s.GetOrCreateModel("mistral").GetAttributes().Put("vendor", testValue{Value: 1})

	models := s.GetModels(func(m datalayer.Model) bool {
		v, ok := m.GetAttributes().Get("vendor")
		return ok && v.(testValue).Value == 1
	})
	if len(models) != 2 {
		t.Errorf("expected 2 models matching predicate, got %d", len(models))
	}

	names := make(map[string]bool)
	for _, m := range models {
		names[m.GetName()] = true
	}
	if !names["gpt-4"] || !names["mistral"] {
		t.Errorf("expected gpt-4 and mistral, got %v", names)
	}
}

// TestGetModelsNoneMatch tests that GetModels returns empty when no models match.
func TestGetModelsNoneMatch(t *testing.T) {
	s := NewDatastore()
	s.GetOrCreateModel("gpt-4")
	s.GetOrCreateModel("llama-3")

	models := s.GetModels(func(datalayer.Model) bool { return false })
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
}

// TestGetModelsEmpty tests that GetModels on an empty store returns empty.
func TestGetModelsEmpty(t *testing.T) {
	s := NewDatastore()
	models := s.GetModels(func(datalayer.Model) bool { return true })
	if len(models) != 0 {
		t.Errorf("expected 0 models on empty store, got %d", len(models))
	}
}

// TestConcurrentAttributeAccess tests concurrent reads and writes to model attributes.
func TestConcurrentAttributeAccess(t *testing.T) {
	s := NewDatastore()
	m := s.GetOrCreateModel("concurrent-model")
	attrs := m.GetAttributes()

	var wg sync.WaitGroup

	// 5 concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", id)
			for j := 0; j < 100; j++ {
				attrs.Put(key, testValue{Value: j})
			}
		}(i)
	}

	// 5 concurrent readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", id)
			for j := 0; j < 100; j++ {
				attrs.Get(key)
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("key-%d", i)
		if _, ok := attrs.Get(key); !ok {
			t.Errorf("expected key %s to exist after concurrent access", key)
		}
	}
}
