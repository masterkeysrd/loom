package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/masterkeysrd/loom/embedding"
	"github.com/tidwall/gjson"
)

// IndexConfig configures semantic vector indexing for a store backend.
type IndexConfig struct {
	// Embed is the embedding model used to vectorize items and search queries.
	Embed *embedding.Model

	// Dims is the vector dimensionality (e.g., 1536).
	// If 0, Dims is inferred automatically from [embedding.Model.Dimensions].
	Dims int

	// Fields specifies the JSON field paths (using gjson path syntax) to extract
	// and embed when storing an item (e.g., []string{"text"} or []string{"meta.summary"}).
	// If empty (or []string{"$"}), the entire JSON payload is embedded.
	Fields []string
}

// IndexEnabled reports whether semantic indexing is configured.
func IndexEnabled(cfg IndexConfig) bool {
	return cfg.Embed != nil
}

// ResolveIndexDims returns the configured vector dimensionality, falling back to
// [embedding.Model.Dimensions] when Dims is not explicitly set.
func ResolveIndexDims(cfg IndexConfig) int {
	if cfg.Dims > 0 {
		return cfg.Dims
	}
	if cfg.Embed != nil {
		return cfg.Embed.Dimensions()
	}
	return 0
}

// EmbedValue extracts indexable text from jsonData according to cfg
// (and any per-call [PutConfig] overrides) and computes its embedding vector.
// Returns (nil, nil) if indexing is disabled or if no non-empty text was extracted.
func EmbedValue(ctx context.Context, cfg IndexConfig, jsonData []byte, putCfg *PutConfig) (embedding.Vector, error) {
	if !IndexEnabled(cfg) {
		return nil, nil
	}
	if putCfg != nil && putCfg.DisableIndex {
		return nil, nil
	}

	fields := cfg.Fields
	if putCfg != nil && len(putCfg.FieldsOverride) > 0 {
		fields = putCfg.FieldsOverride
	}

	text := ExtractText(jsonData, fields)
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	var opts []embedding.CallOption
	if cfg.Dims > 0 {
		opts = append(opts, embedding.WithDimensionsOption(cfg.Dims))
	}

	vecs, err := cfg.Embed.EmbedDocuments(ctx, []string{text}, opts...)
	if err != nil {
		return nil, fmt.Errorf("store: embed document: %w", err)
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	vec := vecs[0]
	if dims := ResolveIndexDims(cfg); dims > 0 && len(vec) != dims {
		return nil, fmt.Errorf("store: embedding dimension mismatch: got %d, expected %d", len(vec), dims)
	}
	return vec, nil
}

// EmbedQuery computes the embedding vector for a search query string.
func EmbedQuery(ctx context.Context, cfg IndexConfig, query string) (embedding.Vector, error) {
	if !IndexEnabled(cfg) {
		return nil, ErrIndexNotConfigured
	}

	var opts []embedding.CallOption
	if cfg.Dims > 0 {
		opts = append(opts, embedding.WithDimensionsOption(cfg.Dims))
	}

	vec, err := cfg.Embed.EmbedQuery(ctx, query, opts...)
	if err != nil {
		return nil, fmt.Errorf("store: embed query: %w", err)
	}
	if dims := ResolveIndexDims(cfg); dims > 0 && len(vec) != dims {
		return nil, fmt.Errorf("store: query embedding dimension mismatch: got %d, expected %d", len(vec), dims)
	}
	return vec, nil
}

// ExtractText extracts text from JSON-encoded data for the given gjson field paths.
// If fields is empty or contains "$", the entire payload is converted to text.
func ExtractText(jsonData []byte, fields []string) string {
	if len(jsonData) == 0 {
		return ""
	}

	if len(fields) == 0 || (len(fields) == 1 && fields[0] == "$") {
		res := gjson.ParseBytes(jsonData)
		if res.Type == gjson.String {
			return res.String()
		}
		return string(jsonData)
	}

	var parts []string
	for _, field := range fields {
		if field == "$" {
			res := gjson.ParseBytes(jsonData)
			if res.Type == gjson.String {
				parts = append(parts, res.String())
			} else {
				parts = append(parts, string(jsonData))
			}
			continue
		}

		res := gjson.GetBytes(jsonData, field)
		if s := formatGJSONResult(res); s != "" {
			parts = append(parts, s)
		}
	}

	return strings.Join(parts, "\n")
}

func formatGJSONResult(res gjson.Result) string {
	if !res.Exists() || res.Type == gjson.Null {
		return ""
	}
	if res.IsArray() {
		var items []string
		for _, elem := range res.Array() {
			if s := formatGJSONResult(elem); s != "" {
				items = append(items, s)
			}
		}
		return strings.Join(items, "\n")
	}
	return res.String()
}
