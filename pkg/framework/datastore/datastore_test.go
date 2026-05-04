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

	if store1 != store2 {
		t.Error("expected same AttributeMap instance")
	}
}

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

func TestDeleteExistingStore(t *testing.T) {
	NewDatastores()

	store, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	store.Put("key", testCloneableValue{Value: 42})

	err = Data.DeleteStore("test-store")
	if err != nil {
		t.Fatalf("expected no error on delete, got %v", err)
	}

	newStore, err := Data.GetOrCreateStore("test-store")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, ok := newStore.Get("key")
	if ok {
		t.Error("expected new store to be empty")
	}
}

func TestDeleteNonExistentStore(t *testing.T) {
	NewDatastores()

	err := Data.DeleteStore("non-existent")
	if err != nil {
		t.Errorf("expected no error for non-existent store, got %v", err)
	}
}

func TestEmptyKeyOnDelete(t *testing.T) {
	NewDatastores()

	err := Data.DeleteStore("")
	if !errors.Is(err, ErrEmptyDatastoreKey) {
		t.Errorf("expected ErrEmptyDatastoreKey, got %v", err)
	}
}

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

func TestConcurrentGetOrCreateStore(t *testing.T) {
	NewDatastores()

	var wg sync.WaitGroup
	stores := make([]AttributeMap, 100)

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

	firstStore := stores[0]
	for i := 1; i < 100; i++ {
		if stores[i] != firstStore {
			t.Errorf("goroutine %d got different store instance", i)
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	NewDatastores()

	var wg sync.WaitGroup

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
}

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
