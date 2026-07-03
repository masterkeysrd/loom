package memorydb_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/masterkeysrd/loom/store"
	"github.com/masterkeysrd/loom/store/memorydb"
)

type testItem struct {
	Name  string   `json:"name"`
	Value int      `json:"value"`
	Tags  []string `json:"tags,omitempty"`
}

func TestPutAndGet(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	item := testItem{Name: "foo", Value: 42, Tags: []string{"a", "b"}}
	if err := s.Put(ctx, "ns", "key1", item); err != nil {
		t.Fatalf("Put: %v", err)
	}

	var got testItem
	if err := s.Get(ctx, "ns", "key1", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != "foo" || got.Value != 42 || len(got.Tags) != 2 {
		t.Fatalf("expected {foo, 42, [a b]}, got {%s, %d, %v}", got.Name, got.Value, got.Tags)
	}
}

func TestGetNotFound(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	var got testItem
	err := s.Get(ctx, "ns", "missing", &got)
	if err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestPutOverwrite(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	item1 := testItem{Name: "first", Value: 1}
	if err := s.Put(ctx, "ns", "key1", item1); err != nil {
		t.Fatalf("Put: %v", err)
	}

	item2 := testItem{Name: "second", Value: 2}
	if err := s.Put(ctx, "ns", "key1", item2); err != nil {
		t.Fatalf("Put: %v", err)
	}

	var got testItem
	if err := s.Get(ctx, "ns", "key1", &got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != "second" || got.Value != 2 {
		t.Fatalf("expected {second, 2}, got {%s, %d}", got.Name, got.Value)
	}
}

func TestSearchAll(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	s.Put(ctx, "ns", "a", testItem{Name: "alpha"})
	s.Put(ctx, "ns", "b", testItem{Name: "beta"})
	s.Put(ctx, "ns", "c", testItem{Name: "gamma"})

	var items []testItem
	if err := s.Search(ctx, "ns", &items); err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
}

func TestSearchWithPrefix(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	s.Put(ctx, "ns", "ab:1", testItem{Name: "one"})
	s.Put(ctx, "ns", "ab:2", testItem{Name: "two"})
	s.Put(ctx, "ns", "cd:1", testItem{Name: "three"})

	var items []testItem
	if err := s.Search(ctx, "ns", &items, store.WithKeyPrefix("ab:")); err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items with prefix ab:, got %d", len(items))
	}
}

func TestSearchWithLimitOffset(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("key%d", i)
		s.Put(ctx, "ns", key, testItem{Name: key})
	}

	var items []testItem
	if err := s.Search(ctx, "ns", &items, store.WithLimit(2), store.WithOffset(1)); err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items with limit=2 offset=1, got %d", len(items))
	}
}

func TestSearchEmptyNamespace(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	var items []testItem
	if err := s.Search(ctx, "nonexistent", &items); err != nil {
		t.Fatalf("Search on empty namespace: %v", err)
	}

	if len(items) != 0 {
		t.Fatalf("expected empty slice, got %d items", len(items))
	}
}

func TestDeleteExisting(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	s.Put(ctx, "ns", "key1", testItem{Name: "foo"})

	if err := s.Delete(ctx, "ns", "key1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var got testItem
	err := s.Get(ctx, "ns", "key1", &got)
	if err != store.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	err := s.Delete(ctx, "ns", "missing")
	if err != nil {
		t.Fatalf("Delete missing key should be nil, got %v", err)
	}
}

func TestNamespaceIsolation(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	s.Put(ctx, "ns1", "key1", testItem{Name: "from ns1"})
	s.Put(ctx, "ns2", "key1", testItem{Name: "from ns2"})

	var items1, items2 []testItem

	if err := s.Search(ctx, "ns1", &items1); err != nil {
		t.Fatalf("Search ns1: %v", err)
	}
	if err := s.Search(ctx, "ns2", &items2); err != nil {
		t.Fatalf("Search ns2: %v", err)
	}

	if len(items1) != 1 || items1[0].Name != "from ns1" {
		t.Fatalf("ns1: expected {from ns1}, got %v", items1)
	}
	if len(items2) != 1 || items2[0].Name != "from ns2" {
		t.Fatalf("ns2: expected {from ns2}, got %v", items2)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := memorydb.New()
	ctx := context.Background()

	var wg sync.WaitGroup

	// Phase 1: concurrent writes.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key%d", n)
			if err := s.Put(ctx, "ns", key, testItem{Name: key, Value: n}); err != nil {
				t.Errorf("Put key%d: %v", n, err)
			}
		}(i)
	}

	wg.Wait()

	// Phase 2: concurrent reads.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key%d", n)
			var got testItem
			if err := s.Get(ctx, "ns", key, &got); err != nil {
				t.Errorf("Get key%d: %v", n, err)
			}
		}(i)
	}

	wg.Wait()

	var items []testItem
	if err := s.Search(ctx, "ns", &items); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 10 {
		t.Fatalf("expected 10 items after concurrent writes, got %d", len(items))
	}
}
