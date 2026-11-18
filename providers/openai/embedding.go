package loomopenai

import (
	"context"
	"fmt"
	"strings"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/profile"
	"github.com/masterkeysrd/loom/stream"
	"github.com/openai/openai-go/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ embedding.Provider = (*Provider)(nil)

// Embed generates vector embeddings for the given request using OpenAI's Embeddings API.
func (p *Provider) Embed(ctx context.Context, request *embedding.Request) (*embedding.Response, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("openai: client is not initialized")
	}
	if request == nil {
		return nil, fmt.Errorf("embedding: request cannot be nil")
	}

	if err := p.validateEmbeddingRequestProfile(request); err != nil {
		return nil, err
	}

	params, err := toEmbeddingNewParams(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert embedding request: %w", err)
	}

	if sw, ok := stream.WriterFromContext(ctx); ok {
		_ = sw.Write(ctx, stream.Event{
			Name: "on_llm_request",
			Data: map[string]any{
				"provider": "openai",
				"payload":  params,
			},
		})
	}

	// OpenAI Specific Span Decoration
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("openai.api.type", "embeddings"),
	)

	res, err := p.client.Embeddings.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create embeddings: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("empty embedding response")
	}

	return toEmbeddingResponse(request, res), nil
}

func toEmbeddingNewParams(request *embedding.Request) (openai.EmbeddingNewParams, error) {
	if request == nil {
		return openai.EmbeddingNewParams{}, fmt.Errorf("embedding: request cannot be nil")
	}
	if len(request.Inputs) == 0 {
		return openai.EmbeddingNewParams{}, fmt.Errorf("embedding: at least one input is required")
	}

	texts := make([]string, len(request.Inputs))
	for i, in := range request.Inputs {
		txt, err := extractOpenAITextWithModel(request.Model, in)
		if err != nil {
			return openai.EmbeddingNewParams{}, fmt.Errorf("input at index %d: %w", i, err)
		}
		texts[i] = txt
	}

	params := openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(request.Model),
		Input: openai.EmbeddingNewParamsInputUnion{
			OfArrayOfStrings: texts,
		},
	}

	if request.Dimensions != nil && *request.Dimensions > 0 {
		params.Dimensions = openai.Int(int64(*request.Dimensions))
	}

	if request.Options != nil {
		if user, ok := request.Options["user"].(string); ok && user != "" {
			params.User = openai.String(user)
		}
	}

	return params, nil
}

func (p *Provider) validateEmbeddingRequestProfile(request *embedding.Request) error {
	if p == nil || request == nil {
		return nil
	}
	prof, ok := p.GetProfile(request.Model)
	if !ok {
		return nil
	}
	if !prof.Capabilities.Embedding && (prof.HasInputModalities() || prof.Capabilities.Completion || prof.Capabilities.Decision) {
		return profile.NewUnsupportedCapabilityError(p.Name(), request.Model, "embedding", "model profile has Embedding=false")
	}
	for i, in := range request.Inputs {
		content := in.Parts
		if in.Text != "" {
			content = append(message.Content{&message.TextBlock{Text: in.Text}}, in.Parts...)
		}
		if err := prof.ValidateContentModalities(p.Name(), request.Model, content); err != nil {
			return fmt.Errorf("input at index %d: %w", i, err)
		}
	}
	return nil
}

func extractOpenAIText(in embedding.Input) (string, error) {
	return extractOpenAITextWithModel("", in)
}

func extractOpenAITextWithModel(model string, in embedding.Input) (string, error) {
	var parts []string
	if in.Text != "" {
		parts = append(parts, in.Text)
	}

	for _, block := range in.Parts {
		switch b := block.(type) {
		case *message.TextBlock:
			if b != nil && b.Text != "" {
				parts = append(parts, b.Text)
			}
		case *message.ImageBlock:
			return "", profile.NewUnsupportedModalityError("openai", model, profile.ModalityImage, "openai embeddings API only supports text inputs")
		case *message.AudioBlock:
			return "", profile.NewUnsupportedModalityError("openai", model, profile.ModalityAudio, "openai embeddings API only supports text inputs")
		case *message.VideoBlock:
			return "", profile.NewUnsupportedModalityError("openai", model, profile.ModalityVideo, "openai embeddings API only supports text inputs")
		case *message.DocumentBlock:
			return "", profile.NewUnsupportedModalityError("openai", model, profile.ModalityPDF, "openai embeddings API only supports text inputs")
		default:
			return "", fmt.Errorf("openai embeddings do not support block type %T", block)
		}
	}

	text := strings.Join(parts, "\n")
	if text == "" {
		return "", fmt.Errorf("embedding input text cannot be empty")
	}
	return text, nil
}

func toEmbeddingResponse(request *embedding.Request, res *openai.CreateEmbeddingResponse) *embedding.Response {
	vecs := make([]embedding.Vector, len(res.Data))
	for i, item := range res.Data {
		v := make(embedding.Vector, len(item.Embedding))
		for j, f := range item.Embedding {
			v[j] = float32(f)
		}
		idx := int(item.Index)
		if idx >= 0 && idx < len(vecs) && vecs[idx] == nil {
			vecs[idx] = v
		} else {
			vecs[i] = v
		}
	}

	model := res.Model
	if model == "" && request != nil {
		model = request.Model
	}

	totalTokens := int(res.Usage.TotalTokens)
	if totalTokens == 0 && res.Usage.PromptTokens > 0 {
		totalTokens = int(res.Usage.PromptTokens)
	}

	return &embedding.Response{
		Model:      model,
		Embeddings: vecs,
		Metrics: &message.TokenMetrics{
			TotalTokens: totalTokens,
			Tokens: message.TokenDetails{
				Input: int(res.Usage.PromptTokens),
			},
		},
	}
}
