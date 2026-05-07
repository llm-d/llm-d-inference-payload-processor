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
	"testing"
)

// TestGetOrCreateStore tests creating new stores and retrieving existing ones.
func TestGetOrCreateStore(t *testing.T) {
	ds := NewDatastores()

	// Create new store
	store1, err := ds.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if store1 == nil {
		t.Fatal("expected non-nil store")
	}

	// Get existing store - should return same instance
	store2, err := ds.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if store1 != store2 {
		t.Error("expected same AttributeMap instance")
	}
}

// TestEmptyKeyHandling tests that empty keys return appropriate errors.
func TestEmptyKeyHandling(t *testing.T) {
	ds := NewDatastores()

	// GetOrCreateStore with empty key
	store, err := ds.GetOrCreateStore("")
	if !errors.Is(err, ErrEmptyDatastoreKey) {
		t.Errorf("expected ErrEmptyDatastoreKey on get, got %v", err)
	}
	if store != nil {
		t.Error("expected nil store for empty key")
	}

	// DeleteStore with empty key
	err = ds.DeleteStore("")
	if !errors.Is(err, ErrEmptyDatastoreKey) {
		t.Errorf("expected ErrEmptyDatastoreKey on delete, got %v", err)
	}
}

// TestDeleteStore tests deleting existing and non-existent stores.
func TestDeleteStore(t *testing.T) {
	ds := NewDatastores()

	// Create and populate a store
	store, err := ds.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	store.Put("key", testCloneableValue{Value: 42})

	// Delete existing store
	err = ds.DeleteStore("test-store")
	if err != nil {
		t.Fatalf("expected no error on delete, got %v", err)
	}

	// Verify new store is empty
	newStore, err := ds.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, ok := newStore.Get("key"); ok {
		t.Error("expected new store to be empty")
	}

	// Delete non-existent store should not error
	err = ds.DeleteStore("non-existent")
	if err != nil {
		t.Errorf("expected no error for non-existent store, got %v", err)
	}
}

// TestMultipleStoresIsolated tests that different stores are isolated from each other.
func TestMultipleStoresIsolated(t *testing.T) {
	ds := NewDatastores()

	store1, err := ds.GetOrCreateStore("store-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	store2, err := ds.GetOrCreateStore("store-2")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	store1.Put("key", testCloneableValue{Value: 1})
	store2.Put("key", testCloneableValue{Value: 2})

	val1, ok := store1.Get("key")
	if !ok {
		t.Fatal("expected key in store1")
	}
	if val1.(testCloneableValue).Value != 1 {
		t.Errorf("expected value 1 in store1, got %d", val1.(testCloneableValue).Value)
	}

	val2, ok := store2.Get("key")
	if !ok {
		t.Fatal("expected key in store2")
	}
	if val2.(testCloneableValue).Value != 2 {
		t.Errorf("expected value 2 in store2, got %d", val2.(testCloneableValue).Value)
	}
}

// TestConcurrentDatastoreAccess tests thread-safety of Datastores operations.
func TestConcurrentDatastoreAccess(t *testing.T) {
	ds := NewDatastores()
	var wg sync.WaitGroup

	// Test concurrent GetOrCreateStore on same key
	stores := make([]AttributeMap, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			store, err := ds.GetOrCreateStore("same-key")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			stores[idx] = store
		}(i)
	}
	wg.Wait()

	// Verify all goroutines got same store instance
	firstStore := stores[0]
	for i := 1; i < 50; i++ {
		if stores[i] != firstStore {
			t.Errorf("goroutine %d got different store instance", i)
		}
	}

	// Test concurrent create and delete operations
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			key := "store-" + string(rune('a'+(idx%10)))
			_, err := ds.GetOrCreateStore(key)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
		go func(idx int) {
			defer wg.Done()
			key := "store-" + string(rune('a'+(idx%10)))
			err := ds.DeleteStore(key)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

// TestNewDatastoresCreatesIndependentInstances tests that each NewDatastores() call
// creates an independent instance with its own isolated stores.
func TestNewDatastoresCreatesIndependentInstances(t *testing.T) {
	ds1 := NewDatastores()

	store1, err := ds1.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	store1.Put("key", testCloneableValue{Value: 42})

	// Create a second independent instance
	ds2 := NewDatastores()

	// The second instance should have an empty store with the same key
	store2, err := ds2.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, ok := store2.Get("key")
	if ok {
		t.Error("expected second instance to have empty store")
	}

	// Verify first instance still has its data
	val, ok := store1.Get("key")
	if !ok {
		t.Fatal("expected key to still exist in first instance")
	}
	if val.(testCloneableValue).Value != 42 {
		t.Errorf("expected value 42 in first instance, got %d", val.(testCloneableValue).Value)
	}
}

// TestDataPersistence tests that data persists across multiple GetOrCreateStore calls.
func TestDataPersistence(t *testing.T) {
	ds := NewDatastores()

	store1, err := ds.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	store1.Put("key", testCloneableValue{Value: 42})

	store2, err := ds.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	val, ok := store2.Get("key")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if val.(testCloneableValue).Value != 42 {
		t.Errorf("expected value 42, got %d", val.(testCloneableValue).Value)
	}
}
