package loomgenai

import (
	"context"
	"fmt"
	"strings"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/genai"
)

var (
	_ embedding.Provider        = (*Provider)(nil)
	_ embedding.ProfileProvider = (*Provider)(nil)
)

// Embed generates vector embeddings for the given request using Google GenAI's EmbedContent API.
func (p *Provider) Embed(ctx context.Context, req *embedding.Request) (*embedding.Response, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("genai: client is not initialized")
	}
	if req == nil {
		return nil, fmt.Errorf("genai: embedding request cannot be nil")
	}

	contents, config, err := toEmbedContentArgs(req)
	if err != nil {
		return nil, fmt.Errorf("genai: build embed request: %w", err)
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "google-genai",
				"payload": map[string]any{
					"model":    req.Model,
					"contents": contents,
					"config":   config,
				},
			},
		})
	}

	// Gemini Specific Span Decoration
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("genai.api.type", "embed_content"),
	)

	resp, err := p.client.Models.EmbedContent(ctx, req.Model, contents, config)
	if err != nil {
		return nil, fmt.Errorf("genai: embed content: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("genai: empty embed content response")
	}

	return toEmbeddingResponse(req, resp), nil
}

func toEmbedContentArgs(req *embedding.Request) ([]*genai.Content, *genai.EmbedContentConfig, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("embedding request cannot be nil")
	}
	if len(req.Inputs) == 0 {
		return nil, nil, fmt.Errorf("at least one embedding input is required")
	}

	contents := make([]*genai.Content, 0, len(req.Inputs))
	for i, in := range req.Inputs {
		var parts []*genai.Part
		if in.Text != "" {
			parts = append(parts, &genai.Part{Text: in.Text})
		}
		if len(in.Parts) > 0 {
			mappedParts, err := toUserParts(in.Parts)
			if err != nil {
				return nil, nil, fmt.Errorf("input at index %d: %w", i, err)
			}
			parts = append(parts, mappedParts...)
		}
		if len(parts) == 0 {
			return nil, nil, fmt.Errorf("input at index %d has no content", i)
		}
		contents = append(contents, &genai.Content{
			Role:  "user",
			Parts: parts,
		})
	}

	config := &genai.EmbedContentConfig{}
	if req.TaskType != embedding.TaskUnspecified {
		config.TaskType = toGenaiTaskType(req.TaskType)
	}
	if req.Dimensions != nil && *req.Dimensions > 0 {
		dims := int32(*req.Dimensions)
		config.OutputDimensionality = &dims
	}
	if req.Options != nil {
		if title, ok := req.Options["title"].(string); ok && title != "" {
			config.Title = title
		}
		if mimeType, ok := req.Options["mime_type"].(string); ok && mimeType != "" {
			config.MIMEType = mimeType
		}
		if autoTruncate, ok := req.Options["auto_truncate"].(bool); ok {
			config.AutoTruncate = autoTruncate
		}
	}

	return contents, config, nil
}

func toGenaiTaskType(t embedding.TaskType) string {
	switch t {
	case embedding.TaskRetrievalQuery:
		return "RETRIEVAL_QUERY"
	case embedding.TaskRetrievalDocument:
		return "RETRIEVAL_DOCUMENT"
	case embedding.TaskSemanticSimilarity:
		return "SEMANTIC_SIMILARITY"
	case embedding.TaskClassification:
		return "CLASSIFICATION"
	case embedding.TaskClustering:
		return "CLUSTERING"
	default:
		return strings.ToUpper(string(t))
	}
}

func toEmbeddingResponse(req *embedding.Request, resp *genai.EmbedContentResponse) *embedding.Response {
	vecs := make([]embedding.Vector, len(resp.Embeddings))
	var totalTokens int
	for i, emb := range resp.Embeddings {
		if emb == nil {
			continue
		}
		v := make(embedding.Vector, len(emb.Values))
		copy(v, emb.Values)
		vecs[i] = v
		if emb.Statistics != nil && emb.Statistics.TokenCount > 0 {
			totalTokens += int(emb.Statistics.TokenCount)
		}
	}

	var metrics *message.TokenMetrics
	if totalTokens > 0 {
		metrics = &message.TokenMetrics{
			TotalTokens: totalTokens,
			Tokens: message.TokenDetails{
				Input: totalTokens,
			},
		}
	}

	model := ""
	if req != nil {
		model = req.Model
	}

	return &embedding.Response{
		Model:      model,
		Embeddings: vecs,
		Metrics:    metrics,
	}
}
