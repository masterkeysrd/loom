package loomollama

import (
	"reflect"
	"slices"
	"testing"

	"github.com/masterkeysrd/loom/profile"
	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/types/model"
)

func TestToModelProfile(t *testing.T) {
	resp := &api.ShowResponse{
		Details: api.ModelDetails{
			Family: "gemma3",
		},
		Parameters:   "num_ctx 8192\ntemperature 0.7",
		Capabilities: []model.Capability{"completion", "vision", "thinking", "tools"},
		ModelInfo: map[string]any{
			"gemma3.context_length": float64(131072),
		},
	}

	prof := toModelProfile("gemma3", resp)

	if prof.ID != "gemma3" {
		t.Errorf("expected ID gemma3, got %s", prof.ID)
	}

	if prof.Family != "gemma3" {
		t.Errorf("expected family gemma3, got %s", prof.Family)
	}

	if !prof.OpenWeights {
		t.Error("expected OpenWeights to be true")
	}

	// ModelInfo should override Parameters
	if prof.Limits.Context != 131072 {
		t.Errorf("expected context limit 131072, got %d", prof.Limits.Context)
	}

	hasVision := slices.Contains(prof.Modalities.Inputs, profile.ModalityImage)
	if !hasVision {
		t.Error("expected vision modality to be present")
	}

	if !prof.Capabilities.Completion {
		t.Error("expected Completion capability to be true")
	}

	if prof.Capabilities.Decision {
		t.Error("expected Decision capability to be false")
	}

	if prof.Capabilities.Embedding {
		t.Error("expected Embedding capability to be false")
	}

	if !prof.Capabilities.Attachment {
		t.Error("expected Attachment capability to be true")
	}

	if !prof.Capabilities.ToolCall {
		t.Error("expected ToolCall capability to be true")
	}

	if !prof.Capabilities.Reasoning {
		t.Error("expected Reasoning capability to be true")
	}
}

func TestToModelProfileDecisionCapability(t *testing.T) {
	resp := &api.ShowResponse{
		Details: api.ModelDetails{
			Family: "nimble",
		},
		Capabilities: []model.Capability{"decision", "vision"},
	}

	profile := toModelProfile("nimble", resp)

	if !profile.Capabilities.Decision {
		t.Error("expected Decision capability to be true")
	}

	if profile.Capabilities.Completion {
		t.Error("expected Completion capability to be false for decision-only model")
	}

	if profile.Capabilities.Embedding {
		t.Error("expected Embedding capability to be false")
	}

	if profile.Capabilities.ToolCall {
		t.Error("expected ToolCall capability to be false")
	}

	if !profile.Capabilities.Attachment {
		t.Error("expected Attachment capability to be true")
	}
}

func TestToModelProfileEmbeddingCapability(t *testing.T) {
	resp := &api.ShowResponse{
		Details: api.ModelDetails{
			Family: "nomic-bert",
		},
		Capabilities: []model.Capability{"embedding"},
		ModelInfo: map[string]any{
			"nomic-bert.context_length":   float64(8192),
			"nomic-bert.embedding_length": float64(768),
		},
	}

	profile := toModelProfile("nomic-embed-text", resp)

	if !profile.Capabilities.Embedding {
		t.Error("expected Embedding capability to be true")
	}

	if profile.Limits.Dimensions != 768 {
		t.Errorf("expected Dimensions limit 768, got %d", profile.Limits.Dimensions)
	}

	if profile.Capabilities.Completion {
		t.Error("expected Completion capability to be false for embedding-only model")
	}

	if profile.Capabilities.Decision {
		t.Error("expected Decision capability to be false")
	}

	if profile.Capabilities.ToolCall {
		t.Error("expected ToolCall capability to be false")
	}
}

func TestToModelProfileFromParameters(t *testing.T) {
	resp := &api.ShowResponse{
		Details: api.ModelDetails{
			Family: "llama3",
		},
		Parameters: "num_ctx 8192",
	}

	profile := toModelProfile("llama3", resp)

	if profile.Limits.Context != 8192 {
		t.Errorf("expected context limit 8192 from parameters, got %d", profile.Limits.Context)
	}
}

func TestToModelProfileDefaultContext(t *testing.T) {
	resp := &api.ShowResponse{
		Details: api.ModelDetails{
			Family: "unknown",
		},
	}

	profile := toModelProfile("unknown", resp)

	if profile.Limits.Context != 4096 {
		t.Errorf("expected default context limit 4096, got %d", profile.Limits.Context)
	}

	if !profile.Capabilities.Completion {
		t.Error("expected default Completion capability to be true when capabilities list is empty")
	}

	if profile.Capabilities.Decision {
		t.Error("expected default Decision capability to be false when capabilities list is empty")
	}

	if profile.Capabilities.Embedding {
		t.Error("expected default Embedding capability to be false when capabilities list is empty")
	}
}

func TestToModelProfileThinkingOptions(t *testing.T) {
	tests := []struct {
		name          string
		thinking      *model.Thinking
		wantReasoning bool
		wantOptions   []profile.ReasoningOption
	}{
		{
			name:          "non-thinking model",
			thinking:      &model.Thinking{Values: []any{false}, Default: false},
			wantReasoning: false,
			wantOptions:   nil,
		},
		{
			name:          "toggle default on",
			thinking:      &model.Thinking{Values: []any{false, true}, Default: true},
			wantReasoning: true,
			wantOptions: []profile.ReasoningOption{
				{Type: "toggle", Default: "true"},
			},
		},
		{
			name:          "toggle default off",
			thinking:      &model.Thinking{Values: []any{false, true}, Default: false},
			wantReasoning: true,
			wantOptions: []profile.ReasoningOption{
				{Type: "toggle", Default: "false"},
			},
		},
		{
			name:          "effort only",
			thinking:      &model.Thinking{Values: []any{"low", "medium", "high"}, Default: "medium"},
			wantReasoning: true,
			wantOptions: []profile.ReasoningOption{
				{Type: "effort", Values: []string{"low", "medium", "high"}, Default: "medium"},
			},
		},
		{
			name:          "mixed toggle and effort",
			thinking:      &model.Thinking{Values: []any{false, "low", "medium", "xhigh"}, Default: "medium"},
			wantReasoning: true,
			wantOptions: []profile.ReasoningOption{
				{Type: "toggle", Default: "true"},
				{Type: "effort", Values: []string{"low", "medium", "xhigh"}, Default: "medium"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &api.ShowResponse{
				Details:  api.ModelDetails{Family: "test"},
				Thinking: tt.thinking,
			}
			prof := toModelProfile("test-model", resp)
			if prof.Capabilities.Reasoning != tt.wantReasoning {
				t.Errorf("Reasoning = %v, want %v", prof.Capabilities.Reasoning, tt.wantReasoning)
			}
			if !reflect.DeepEqual(prof.Capabilities.ReasoningOptions, tt.wantOptions) {
				t.Errorf("ReasoningOptions = %#v, want %#v", prof.Capabilities.ReasoningOptions, tt.wantOptions)
			}
		})
	}
}
