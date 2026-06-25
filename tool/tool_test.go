package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/masterkeysrd/loom/message"
)

func TestContainerCallStructured(t *testing.T) {
	myTool, _ := New("test", "test", "test", func(ctx context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"result": "ok"}, nil
	})

	c := NewContainer(myTool)
	res, err := c.Call(context.Background(), &message.ToolCall{
		Name: "test",
		Args: map[string]any{},
	})

	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	if res.StructuredContent == nil {
		t.Errorf("expected structured content to be populated")
	}

	resMap, ok := res.StructuredContent.(map[string]any)
	if !ok || resMap["result"] != "ok" {
		t.Errorf("expected structured content to be map[string]any{\"result\": \"ok\"}")
	}
}

func TestErrToolNotFound(t *testing.T) {
	c := NewContainer()
	_, err := c.Call(context.Background(), &message.ToolCall{
		Name: "missing_tool",
		Args: map[string]any{},
	})

	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrToolNotFound) {
		t.Errorf("expected ErrToolNotFound, got %v", err)
	}
}

type validatableInput struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func (v validatableInput) Validate() error {
	if v.Age < 18 {
		return errors.New("must be at least 18")
	}
	return nil
}

func TestValidator(t *testing.T) {
	myTool, err := New("test_validator", "test", "test", func(ctx context.Context, in validatableInput) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("failed to create tool: %v", err)
	}

	// 1. Test manual Validate method
	err = myTool.Validate(map[string]any{
		"name": "Alice",
		"age":  float64(17), // JSON unmarshals numbers to float64 for map[string]any
	})
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}

	err = myTool.Validate(map[string]any{
		"name": "Bob",
		"age":  float64(20),
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// 2. Test during execution
	c := NewContainer(myTool)
	_, err = c.Call(context.Background(), &message.ToolCall{
		Name: "test_validator",
		Args: map[string]any{
			"name": "Charlie",
			"age":  float64(16),
		},
	})
	if err == nil {
		t.Fatalf("expected execution validation error, got nil")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected execution error to wrap ErrInvalidInput, got %v", err)
	}

	_, err = c.Call(context.Background(), &message.ToolCall{
		Name: "test_validator",
		Args: map[string]any{
			"name": "Dave",
			"age":  float64(25),
		},
	})
	if err != nil {
		t.Errorf("expected execution to succeed, got %v", err)
	}
}
