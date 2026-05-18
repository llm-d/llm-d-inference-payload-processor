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

// TestPutEdgeCases tests edge cases for Put operation.
func TestPutEdgeCases(t *testing.T) {
	am := NewAttributes()

	// Empty key should be no-op
	am.Put("", testCloneableValue{Value: 42})
	if len(am.Keys()) != 0 {
		t.Error("expected empty key to be ignored")
	}

	// Nil value should be no-op
	am.Put("test", nil)
	if _, ok := am.Get("test"); ok {
		t.Error("expected nil value to be ignored")
	}

	// Update existing key
	am.Put("key", testCloneableValue{Value: 1})
	am.Put("key", testCloneableValue{Value: 2})
	if val, _ := am.Get("key"); val.(testCloneableValue).Value != 2 {
		t.Error("expected key to be updated")
	}
}

// TestDelete tests deleting keys from AttributeMap.
func TestDelete(t *testing.T) {
	am := NewAttributes()
	am.Put("test", testCloneableValue{Value: 42})

	am.Delete("test")
	if _, ok := am.Get("test"); ok {
		t.Error("expected key to be deleted")
	}

	// Deleting non-existent key should not panic
	am.Delete("non-existent")
}

// TestKeys tests retrieving all keys from AttributeMap.
func TestKeys(t *testing.T) {
	am := NewAttributes()

	am.Put("key1", testCloneableValue{Value: 1})
	am.Put("key2", testCloneableValue{Value: 2})
	am.Put("key3", testCloneableValue{Value: 3})

	keys := am.Keys()
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}

	// Verify all keys are present
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

// TestCloneIndependence tests that cloning creates independent copies.
func TestCloneIndependence(t *testing.T) {
	am := NewAttributes()
	am.Put("key1", testCloneableValue{Value: 1})
	am.Put("key2", testCloneableValue{Value: 2})

	clone := am.Clone()

	// Verify clone has same data
	if len(clone.Keys()) != 2 {
		t.Error("expected clone to have 2 keys")
	}
	if val, _ := clone.Get("key1"); val.(testCloneableValue).Value != 1 {
		t.Error("expected clone to have same values")
	}

	// Modify clone
	clone.Put("key1", testCloneableValue{Value: 99})
	clone.Put("key3", testCloneableValue{Value: 3})

	// Original should be unchanged
	if val, _ := am.Get("key1"); val.(testCloneableValue).Value != 1 {
		t.Error("expected original to be unchanged")
	}
	if _, ok := am.Get("key3"); ok {
		t.Error("expected key3 to not exist in original")
	}
}

// TestConcurrentAccess tests thread-safety of AttributeMap.
func TestConcurrentAccess(t *testing.T) {
	am := NewAttributes()
	var wg sync.WaitGroup

	// Concurrent Put, Get, and Delete operations
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func(val int) {
			defer wg.Done()
			key := string(rune('a' + (val % 26)))
			am.Put(key, testCloneableValue{Value: val})
		}(i)
		go func(val int) {
			defer wg.Done()
			key := string(rune('a' + (val % 26)))
			am.Get(key)
		}(i)
		go func(val int) {
			defer wg.Done()
			key := string(rune('a' + (val % 26)))
			am.Delete(key)
		}(i)
	}

	wg.Wait()
	// Test passes if no panics or race conditions occurred
}

// TestReadAttributeKey verifies success case: key is found and value is of expected type.
// Verifies error case: key is not found.
// Verifies error case: value is not of expected type.
func TestReadAttributeKey(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		attrs := NewAttributes()
		expected := testCloneableValue{Value: 42}
		attrs.Put("score", expected)

		got, err := ReadAttributeKey[testCloneableValue](attrs, "score")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if got != expected {
			t.Fatalf("expected value %+v, got %+v", expected, got)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		attrs := NewAttributes()

		got, err := ReadAttributeKey[testCloneableValue](attrs, "missing")
		if err == nil {
			t.Fatal("expected error for missing key")
		}
		if got != (testCloneableValue{}) {
			t.Fatalf("expected zero value, got %+v", got)
		}
		if err.Error() != `attribute "missing": not found` {
			t.Fatalf("expected not found error, got %v", err)
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		attrs := NewAttributes()
		attrs.Put("score", testCloneableValue{Value: 42})

		got, err := ReadAttributeKey[*testCloneableValue](attrs, "score")
		if err == nil {
			t.Fatal("expected error for wrong type")
		}
		if got != nil {
			t.Fatalf("expected zero value nil, got %+v", got)
		}
		expectedErrPrefix := `unexpected type for key "score": got`
		if len(err.Error()) < len(expectedErrPrefix) || err.Error()[:len(expectedErrPrefix)] != expectedErrPrefix {
			t.Fatalf("expected type error, got %v", err)
		}
	})

	t.Run("pointer type", func(t *testing.T) {
		attrs := NewAttributes()
		// Store a pointer type that implements Cloneable
		expected := &testCloneablePointer{Value: 99}
		attrs.Put("ptr", expected)

		got, err := ReadAttributeKey[*testCloneablePointer](attrs, "ptr")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if got == nil {
			t.Fatal("expected non-nil pointer")
		}
		if got.Value != 99 {
			t.Fatalf("expected value 99, got %d", got.Value)
		}
	})

	t.Run("value type from pointer storage", func(t *testing.T) {
		attrs := NewAttributes()
		// Store a pointer, but Clone() returns value type
		attrs.Put("mixed", &testCloneablePointer{Value: 42})

		// This should work if Clone() returns the value type
		// But will fail if Clone() returns pointer type
		// This test documents the expected behavior
		_, err := ReadAttributeKey[testCloneablePointer](attrs, "mixed")
		// We expect this to fail because Clone() returns *testCloneablePointer
		if err == nil {
			t.Fatal("expected error when type mismatch between stored and requested")
		}
	})
}

// testCloneablePointer is a pointer type that implements Cloneable
type testCloneablePointer struct {
	Value int
}

func (t *testCloneablePointer) Clone() Cloneable {
	if t == nil {
		return nil
	}
	return &testCloneablePointer{Value: t.Value}
}
