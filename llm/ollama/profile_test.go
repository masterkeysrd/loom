package loomollama

import (
	"testing"

	"github.com/masterkeysrd/loom/llm"
	"github.com/ollama/ollama/api"
	"github.com/ollama/ollama/types/model"
)

func TestToModelProfile(t *testing.T) {
	resp := &api.ShowResponse{
		Details: api.ModelDetails{
			Family: "gemma3",
		},
		Parameters:   "num_ctx 8192\ntemperature 0.7",
		Capabilities: []model.Capability{"completion", "vision"},
		ModelInfo: map[string]any{
			"gemma3.context_length": float64(131072),
		},
	}

	profile := toModelProfile("gemma3", resp)

	if profile.ID != "gemma3" {
		t.Errorf("expected ID gemma3, got %s", profile.ID)
	}

	if profile.Family != "gemma3" {
		t.Errorf("expected family gemma3, got %s", profile.Family)
	}

	// ModelInfo should override Parameters
	if profile.Limits.Context != 131072 {
		t.Errorf("expected context limit 131072, got %d", profile.Limits.Context)
	}

	hasVision := false
	for _, m := range profile.Modalities.Inputs {
		if m == llm.ModalityImage {
			hasVision = true
			break
		}
	}
	if !hasVision {
		t.Error("expected vision modality to be present")
	}

	if !profile.Capabilities.Attachment {
		t.Error("expected Attachment capability to be true")
	}

	if !profile.Capabilities.ToolCall {
		t.Error("expected ToolCall capability to be true")
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
}
