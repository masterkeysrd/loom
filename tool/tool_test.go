package tool

import (
	"context"
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
