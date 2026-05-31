package llm

import (
	"context"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/masterkeysrd/loom/message"
)

type mockProvider struct {
	lastRequest *Request
	Provider
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Stream(ctx context.Context, req *Request) (StreamResponse, error) {
	m.lastRequest = req
	return func(yield func(message.AssistantChunk, error) bool) {}, nil
}
func (m *mockProvider) GetConfig(modelID string) (ModelConfig, bool) { return ModelConfig{}, false }

func TestWithStructuredOutput(t *testing.T) {
	provider := &mockProvider{}
	model, _ := NewModel(provider, "test-model", nil)

	schema, _ := jsonschema.For[map[string]string](nil)
	schema.Title = "TestSchema"
	schema.Description = "A test schema"

	model = model.WithStructuredOutput(schema)

	if model.config.ResponseSchema != schema {
		t.Fatal("ResponseSchema not set correctly in config")
	}

	model.Invoke(context.Background(), []message.Message{message.NewUserText("hi")})

	if provider.lastRequest == nil {
		t.Fatal("Request not sent to provider")
	}

	if provider.lastRequest.ResponseSchema != schema {
		t.Error("ResponseSchema not passed to provider request")
	}
}
