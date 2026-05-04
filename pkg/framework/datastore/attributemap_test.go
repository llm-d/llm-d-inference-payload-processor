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
)

// testCloneableValue is a test implementation of Cloneable interface
type testCloneableValue struct {
	Value int
}

func (t testCloneableValue) Clone() Cloneable {
	return testCloneableValue{Value: t.Value}
}

// TestPutAndGet tests storing and retrieving a value from AttributeMap.
func TestPutAndGet(t *testing.T) {
	am := NewAttributes()
	testValue := testCloneableValue{Value: 42}

	am.Put("test", testValue)

	got, ok := am.Get("test")
	if !ok {
		t.Fatal("expected key 'test' to exist")
	}

	gotValue, ok := got.(testCloneableValue)
	if !ok {
		t.Fatal("expected value to be testCloneableValue")
	}

	if gotValue.Value != 42 {
		t.Errorf("expected value 42, got %d", gotValue.Value)
	}
}

// TestGetNonExistent tests retrieving a non-existent key returns nil and false.
func TestGetNonExistent(t *testing.T) {
	am := NewAttributes()

	got, ok := am.Get("missing")
	if ok {
		t.Error("expected ok to be false for non-existent key")
	}
	if got != nil {
		t.Error("expected nil value for non-existent key")
	}
}

// TestPutEmptyKey tests that putting with an empty key is a no-op.
func TestPutEmptyKey(t *testing.T) {
	am := NewAttributes()
	testValue := testCloneableValue{Value: 42}

	am.Put("", testValue)

	keys := am.Keys()
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

// TestPutNilValue tests that putting a nil value is a no-op.
func TestPutNilValue(t *testing.T) {
	am := NewAttributes()

	am.Put("test", nil)

	got, ok := am.Get("test")
	if ok {
		t.Error("expected ok to be false for nil value")
	}
	if got != nil {
		t.Error("expected nil value")
	}
}

// TestDeleteExisting tests deleting an existing key.
func TestDeleteExisting(t *testing.T) {
	am := NewAttributes()
	testValue := testCloneableValue{Value: 42}

	am.Put("test", testValue)
	am.Delete("test")

	got, ok := am.Get("test")
	if ok {
		t.Error("expected ok to be false after delete")
	}
	if got != nil {
		t.Error("expected nil value after delete")
	}
}

// TestDeleteNonExistent tests that deleting a non-existent key is a no-op.
func TestDeleteNonExistent(t *testing.T) {
	am := NewAttributes()

	// Should not panic
	am.Delete("non-existent")
}

// TestKeysOnEmptyMap tests that Keys() returns an empty slice on a new AttributeMap.
func TestKeysOnEmptyMap(t *testing.T) {
	am := NewAttributes()

	keys := am.Keys()
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

// TestKeysWithData tests that Keys() returns all stored keys.
func TestKeysWithData(t *testing.T) {
	am := NewAttributes()

	am.Put("key1", testCloneableValue{Value: 1})
	am.Put("key2", testCloneableValue{Value: 2})
	am.Put("key3", testCloneableValue{Value: 3})

	keys := am.Keys()
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}

	// Verify all keys are present (order not guaranteed)
	keyMap := make(map[string]bool)
	for _, k := range keys {
		keyMap[k] = true
	}

	for _, expectedKey := range []string{"key1", "key2", "key3"} {
		if !keyMap[expectedKey] {
			t.Errorf("expected key %q to be present", expectedKey)
		}
	}
}

// TestCloneEmptyMap tests cloning an empty AttributeMap.
func TestCloneEmptyMap(t *testing.T) {
	am := NewAttributes()

	clone := am.Clone()

	keys := clone.Keys()
	if len(keys) != 0 {
		t.Errorf("expected 0 keys in clone, got %d", len(keys))
	}
}

// TestCloneWithData tests cloning an AttributeMap with data.
func TestCloneWithData(t *testing.T) {
	am := NewAttributes()

	am.Put("key1", testCloneableValue{Value: 1})
	am.Put("key2", testCloneableValue{Value: 2})

	clone := am.Clone()

	// Verify clone has same keys
	keys := clone.Keys()
	if len(keys) != 2 {
		t.Errorf("expected 2 keys in clone, got %d", len(keys))
	}

	// Verify values
	val1, ok := clone.Get("key1")
	if !ok {
		t.Fatal("expected key1 in clone")
	}
	if val1.(testCloneableValue).Value != 1 {
		t.Errorf("expected value 1, got %d", val1.(testCloneableValue).Value)
	}

	val2, ok := clone.Get("key2")
	if !ok {
		t.Fatal("expected key2 in clone")
	}
	if val2.(testCloneableValue).Value != 2 {
		t.Errorf("expected value 2, got %d", val2.(testCloneableValue).Value)
	}
}

// TestCloneIndependence tests that modifying a clone doesn't affect the original.
func TestCloneIndependence(t *testing.T) {
	am := NewAttributes()
	am.Put("key1", testCloneableValue{Value: 1})

	clone := am.Clone()
	clone.Put("key1", testCloneableValue{Value: 99})
	clone.Put("key2", testCloneableValue{Value: 2})

	// Original should be unchanged
	val, ok := am.Get("key1")
	if !ok {
		t.Fatal("expected key1 in original")
	}
	if val.(testCloneableValue).Value != 1 {
		t.Errorf("expected original value 1, got %d", val.(testCloneableValue).Value)
	}

	// Original should not have key2
	_, ok = am.Get("key2")
	if ok {
		t.Error("expected key2 to not exist in original")
	}
}

// TestGetReturnsClone tests that Get() returns independent clones.
func TestGetReturnsClone(t *testing.T) {
	am := NewAttributes()
	am.Put("test", testCloneableValue{Value: 42})

	// Get twice
	val1, ok1 := am.Get("test")
	val2, ok2 := am.Get("test")

	if !ok1 || !ok2 {
		t.Fatal("expected both Gets to succeed")
	}

	// Modify first value
	v1 := val1.(testCloneableValue)
	v1.Value = 99

	// Second value should be unchanged
	v2 := val2.(testCloneableValue)
	if v2.Value != 42 {
		t.Errorf("expected second value to be 42, got %d", v2.Value)
	}
}

// TestUpdateExistingKey tests that putting a key twice overwrites the first value.
func TestUpdateExistingKey(t *testing.T) {
	am := NewAttributes()

	am.Put("test", testCloneableValue{Value: 1})
	am.Put("test", testCloneableValue{Value: 2})

	val, ok := am.Get("test")
	if !ok {
		t.Fatal("expected key 'test' to exist")
	}

	if val.(testCloneableValue).Value != 2 {
		t.Errorf("expected value 2, got %d", val.(testCloneableValue).Value)
	}
}

// TestMultipleKeys tests storing and retrieving multiple different keys.
func TestMultipleKeys(t *testing.T) {
	am := NewAttributes()

	// Put 10 different keys
	for i := 0; i < 10; i++ {
		key := string(rune('a' + i))
		am.Put(key, testCloneableValue{Value: i})
	}

	// Verify all keys are retrievable
	for i := 0; i < 10; i++ {
		key := string(rune('a' + i))
		val, ok := am.Get(key)
		if !ok {
			t.Errorf("expected key %q to exist", key)
			continue
		}
		if val.(testCloneableValue).Value != i {
			t.Errorf("expected value %d for key %q, got %d", i, key, val.(testCloneableValue).Value)
		}
	}

	// Verify Keys() returns 10 keys
	keys := am.Keys()
	if len(keys) != 10 {
		t.Errorf("expected 10 keys, got %d", len(keys))
	}
}

// TestConcurrentPutGetDelete tests concurrent operations on AttributeMap.
func TestConcurrentPutGetDelete(t *testing.T) {
	am := NewAttributes()
	var wg sync.WaitGroup

	// 100 goroutines doing Put
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			key := string(rune('a' + (val % 26)))
			am.Put(key, testCloneableValue{Value: val})
		}(i)
	}

	// 100 goroutines doing Get
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			key := string(rune('a' + (val % 26)))
			am.Get(key)
		}(i)
	}

	// 100 goroutines doing Delete
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			key := string(rune('a' + (val % 26)))
			am.Delete(key)
		}(i)
	}

	wg.Wait()

	// Test passes if no panics or race conditions occurred
}
