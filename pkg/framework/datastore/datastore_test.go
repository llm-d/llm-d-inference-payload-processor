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
	NewDatastores()

	// Create new store
	store1, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if store1 == nil {
		t.Fatal("expected non-nil store")
	}

	// Get existing store - should return same instance
	store2, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if store1 != store2 {
		t.Error("expected same AttributeMap instance")
	}
}

// TestEmptyKeyHandling tests that empty keys return appropriate errors.
func TestEmptyKeyHandling(t *testing.T) {
	NewDatastores()

	// GetOrCreateStore with empty key
	store, err := Data.GetOrCreateStore("")
	if !errors.Is(err, ErrEmptyDatastoreKey) {
		t.Errorf("expected ErrEmptyDatastoreKey on get, got %v", err)
	}
	if store != nil {
		t.Error("expected nil store for empty key")
	}

	// DeleteStore with empty key
	err = Data.DeleteStore("")
	if !errors.Is(err, ErrEmptyDatastoreKey) {
		t.Errorf("expected ErrEmptyDatastoreKey on delete, got %v", err)
	}
}

// TestDeleteStore tests deleting existing and non-existent stores.
func TestDeleteStore(t *testing.T) {
	NewDatastores()

	// Create and populate a store
	store, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	store.Put("key", testCloneableValue{Value: 42})

	// Delete existing store
	err = Data.DeleteStore("test-store")
	if err != nil {
		t.Fatalf("expected no error on delete, got %v", err)
	}

	// Verify new store is empty
	newStore, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, ok := newStore.Get("key"); ok {
		t.Error("expected new store to be empty")
	}

	// Delete non-existent store should not error
	err = Data.DeleteStore("non-existent")
	if err != nil {
		t.Errorf("expected no error for non-existent store, got %v", err)
	}
}

// TestMultipleStoresIsolated tests that different stores are isolated from each other.
func TestMultipleStoresIsolated(t *testing.T) {
	NewDatastores()

	store1, err := Data.GetOrCreateStore("store-1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	store2, err := Data.GetOrCreateStore("store-2")
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
	NewDatastores()
	var wg sync.WaitGroup

	// Test concurrent GetOrCreateStore on same key
	stores := make([]AttributeMap, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			store, err := Data.GetOrCreateStore("same-key")
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
			_, err := Data.GetOrCreateStore(key)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
		go func(idx int) {
			defer wg.Done()
			key := "store-" + string(rune('a'+(idx%10)))
			err := Data.DeleteStore(key)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

// TestNewDatastoresResets tests that NewDatastores() clears all existing stores.
func TestNewDatastoresResets(t *testing.T) {
	NewDatastores()

	store, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	store.Put("key", testCloneableValue{Value: 42})

	NewDatastores()

	newStore, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, ok := newStore.Get("key")
	if ok {
		t.Error("expected new store to be empty after NewDatastores()")
	}
}

// TestDataPersistence tests that data persists across multiple GetOrCreateStore calls.
func TestDataPersistence(t *testing.T) {
	NewDatastores()

	store1, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	store1.Put("key", testCloneableValue{Value: 42})

	store2, err := Data.GetOrCreateStore("test-store")
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
