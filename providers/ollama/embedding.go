package loomollama

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/message"
	"github.com/ollama/ollama/api"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ embedding.Provider = (*Provider)(nil)

// Embed generates vector embeddings for the given request using Ollama's /api/embed endpoint.
func (p *Provider) Embed(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
	if p == nil || p.client == nil {
		return nil, errors.New("ollama: client is not initialized")
	}
	if req == nil {
		return nil, errors.New("ollama: embedding request cannot be nil")
	}

	embedReq, err := toEmbedRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build embed request: %w", err)
	}

	// Decorate OTel span with ollama.api.type = "embed" and server info
	span := trace.SpanFromContext(ctx)
	attrs := []attribute.KeyValue{
		attribute.String("ollama.api.type", "embed"),
	}
	if p.baseURL != nil {
		attrs = append(attrs, attribute.String("server.address", p.baseURL.Hostname()))
		if portStr := p.baseURL.Port(); portStr != "" {
			if port, err := strconv.Atoi(portStr); err == nil {
				attrs = append(attrs, attribute.Int("server.port", port))
			}
		} else if p.baseURL.Scheme == "https" {
			attrs = append(attrs, attribute.Int("server.port", 443))
		} else if p.baseURL.Scheme == "http" {
			attrs = append(attrs, attribute.Int("server.port", 80))
		}
	}
	span.SetAttributes(attrs...)

	resp, err := p.client.Embed(ctx, embedReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute embed call: %w", err)
	}
	if resp == nil {
		return nil, errors.New("received nil response from ollama embed")
	}

	return toEmbeddingResponse(resp, req), nil
}

func toEmbedRequest(req *embedding.Request) (*api.EmbedRequest, error) {
	if len(req.Inputs) == 0 {
		return nil, errors.New("embedding request must contain at least one input")
	}

	inputs := make([]string, len(req.Inputs))
	for i, in := range req.Inputs {
		text, err := extractOllamaText(in)
		if err != nil {
			return nil, fmt.Errorf("input at index %d: %w", i, err)
		}
		inputs[i] = text
	}

	var dims int
	if req.Dimensions != nil && *req.Dimensions > 0 {
		dims = *req.Dimensions
	}

	embedReq := &api.EmbedRequest{
		Model:      req.Model,
		Input:      inputs,
		Dimensions: dims,
		Options:    req.Options,
	}

	if req.Options != nil {
		if runner, ok := req.Options["runner"].(string); ok && runner != "" {
			embedReq.Runner = runner
		}
		if truncate, ok := req.Options["truncate"].(bool); ok {
			embedReq.Truncate = &truncate
		}
	}

	return embedReq, nil
}

func extractOllamaText(in embedding.Input) (string, error) {
	var parts []string
	if in.Text != "" {
		parts = append(parts, in.Text)
	}

	for _, block := range in.Parts {
		switch b := block.(type) {
		case *message.TextBlock:
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		default:
			return "", fmt.Errorf("ollama embed does not support multimodal block type %T", block)
		}
	}

	text := strings.Join(parts, "\n")
	if text == "" {
		return "", errors.New("embedding input text cannot be empty")
	}
	return text, nil
}

func toEmbeddingResponse(resp *api.EmbedResponse, req *embedding.Request) *embedding.Response {
	vecs := make([]embedding.Vector, len(resp.Embeddings))
	for i, raw := range resp.Embeddings {
		vec := make(embedding.Vector, len(raw))
		copy(vec, raw)
		vecs[i] = vec
	}

	modelName := resp.Model
	if modelName == "" && req != nil {
		modelName = req.Model
	}

	var metrics *message.TokenMetrics
	if resp.PromptEvalCount > 0 || resp.TotalDuration > 0 {
		metrics = &message.TokenMetrics{
			TotalTokens: resp.PromptEvalCount,
			Tokens: message.TokenDetails{
				Input: resp.PromptEvalCount,
			},
			Timing: message.TimingMetrics{
				Total: resp.TotalDuration,
			},
		}
	}

	return &embedding.Response{
		Model:      modelName,
		Embeddings: vecs,
		Metrics:    metrics,
	}
}
