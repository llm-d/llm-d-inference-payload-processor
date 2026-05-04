/*
Copyright 2025 The Kubernetes Authors.

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

// TestCreateNewStore tests creating a new store with GetOrCreateStore.
func TestCreateNewStore(t *testing.T) {
	NewDatastores()

	store, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

// TestGetExistingStore tests that GetOrCreateStore returns the same instance for existing stores.
func TestGetExistingStore(t *testing.T) {
	NewDatastores()

	store1, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	store2, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify same instance
	if store1 != store2 {
		t.Error("expected same AttributeMap instance")
	}
}

// TestEmptyDatastoreKey tests that empty datastoreKey returns ErrEmptyDatastoreKey.
func TestEmptyDatastoreKey(t *testing.T) {
	NewDatastores()

	store, err := Data.GetOrCreateStore("")
	if !errors.Is(err, ErrEmptyDatastoreKey) {
		t.Errorf("expected ErrEmptyDatastoreKey, got %v", err)
	}
	if store != nil {
		t.Error("expected nil store for empty key")
	}
}

// TestDeleteExistingStore tests deleting an existing store.
func TestDeleteExistingStore(t *testing.T) {
	NewDatastores()

	// Create store
	store, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Put some data
	store.Put("key", testCloneableValue{Value: 42})

	// Delete store
	err = Data.DeleteStore("test-store")
	if err != nil {
		t.Fatalf("expected no error on delete, got %v", err)
	}

	// Create again should give new empty store
	newStore, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify it's a new store (no data)
	_, ok := newStore.Get("key")
	if ok {
		t.Error("expected new store to be empty")
	}
}

// TestDeleteNonExistentStore tests that deleting a non-existent store is a no-op.
func TestDeleteNonExistentStore(t *testing.T) {
	NewDatastores()

	err := Data.DeleteStore("non-existent")
	if err != nil {
		t.Errorf("expected no error for non-existent store, got %v", err)
	}
}

// TestEmptyKeyOnDelete tests that empty datastoreKey on delete returns ErrEmptyDatastoreKey.
func TestEmptyKeyOnDelete(t *testing.T) {
	NewDatastores()

	err := Data.DeleteStore("")
	if !errors.Is(err, ErrEmptyDatastoreKey) {
		t.Errorf("expected ErrEmptyDatastoreKey, got %v", err)
	}
}

// TestMultipleStoresIsolated tests that multiple stores maintain independent data.
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

	// Put different data in each store
	store1.Put("key", testCloneableValue{Value: 1})
	store2.Put("key", testCloneableValue{Value: 2})

	// Verify isolation
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

// TestConcurrentGetOrCreateStore tests concurrent GetOrCreateStore calls.
func TestConcurrentGetOrCreateStore(t *testing.T) {
	NewDatastores()

	var wg sync.WaitGroup
	stores := make([]AttributeMap, 100)

	// 100 goroutines calling GetOrCreateStore with same key
	for i := 0; i < 100; i++ {
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

	// Verify all goroutines got the same instance
	firstStore := stores[0]
	for i := 1; i < 100; i++ {
		if stores[i] != firstStore {
			t.Errorf("goroutine %d got different store instance", i)
		}
	}
}

// TestConcurrentOperations tests concurrent GetOrCreateStore and DeleteStore operations.
func TestConcurrentOperations(t *testing.T) {
	NewDatastores()

	var wg sync.WaitGroup

	// 50 goroutines doing GetOrCreateStore
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := "store-" + string(rune('a'+(idx%10)))
			_, err := Data.GetOrCreateStore(key)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}(i)
	}

	// 50 goroutines doing DeleteStore
	for i := 0; i < 50; i++ {
		wg.Add(1)
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

	// Test passes if no panics or deadlocks occurred
}

// TestNewDatastoresResets tests that calling NewDatastores() clears previous stores.
func TestNewDatastoresResets(t *testing.T) {
	NewDatastores()

	// Create a store and put data
	store, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	store.Put("key", testCloneableValue{Value: 42})

	// Call NewDatastores again
	NewDatastores()

	// Get store again - should be new empty store
	newStore, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify it's empty
	_, ok := newStore.Get("key")
	if ok {
		t.Error("expected new store to be empty after NewDatastores()")
	}
}

// TestDataPersistence tests that data persists across GetOrCreateStore calls.
func TestDataPersistence(t *testing.T) {
	NewDatastores()

	// Create store and put data
	store1, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	store1.Put("key", testCloneableValue{Value: 42})

	// Get store again
	store2, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify data persists
	val, ok := store2.Get("key")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if val.(testCloneableValue).Value != 42 {
		t.Errorf("expected value 42, got %d", val.(testCloneableValue).Value)
	}
}
