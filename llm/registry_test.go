package llm_test

import (
	"errors"
	"testing"

	"github.com/masterkeysrd/loom/llm"
)

type mockProvider struct {
	llm.Provider
}

func TestRegistry_LazyLoading(t *testing.T) {
	registry := llm.NewRegistry()
	callCount := 0
	factory := func() (llm.Provider, error) {
		callCount++
		return &mockProvider{}, nil
	}

	registry.Register("test", factory)

	if callCount != 0 {
		t.Errorf("expected callCount 0 before Get, got %d", callCount)
	}

	p1, err := registry.Get("test")
	if err != nil {
		t.Fatalf("unexpected error on Get: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected callCount 1 after first Get, got %d", callCount)
	}

	p2, err := registry.Get("test")
	if err != nil {
		t.Fatalf("unexpected error on second Get: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected callCount 1 after second Get (should be cached), got %d", callCount)
	}

	if p1 != p2 {
		t.Errorf("expected same provider instance on subsequent Gets")
	}
}

func TestRegistry_ErrorHandling(t *testing.T) {
	registry := llm.NewRegistry()
	errTest := errors.New("factory failed")
	factory := func() (llm.Provider, error) {
		return nil, errTest
	}

	registry.Register("test-fail", factory)

	_, err := registry.Get("test-fail")
	if !errors.Is(err, errTest) {
		t.Errorf("expected error %v, got %v", errTest, err)
	}

	// Ensure the error is also cached
	_, err2 := registry.Get("test-fail")
	if !errors.Is(err2, errTest) {
		t.Errorf("expected cached error %v on second Get, got %v", errTest, err2)
	}
}

func TestRegistry_NotFound(t *testing.T) {
	registry := llm.NewRegistry()
	_, err := registry.Get("unknown")
	if err == nil {
		t.Error("expected error for non-existent provider")
	}
}
