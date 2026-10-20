package document

// Document represents a piece of text with associated metadata, an optional
// unique identifier, and an optional relevance score.
type Document struct {
	ID       string         `json:"id,omitempty"`
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Score    float32        `json:"score,omitempty"`
}

// New creates a new Document with the provided content and optional metadata maps.
// If metadata maps are provided, their entries are merged into a newly allocated
// map so that subsequent modifications do not mutate the document.
func New(content string, metadata ...map[string]any) Document {
	return Document{
		Content:  content,
		Metadata: mergeMetadata(metadata...),
	}
}

func mergeMetadata(maps ...map[string]any) map[string]any {
	var meta map[string]any
	for _, m := range maps {
		if len(m) == 0 {
			continue
		}
		if meta == nil {
			meta = make(map[string]any, len(m))
		}
		for k, v := range m {
			meta[k] = v
		}
	}
	return meta
}

// WithID returns a shallow copy of the document with the given identifier.
func (d Document) WithID(id string) Document {
	d.ID = id
	return d
}

// WithScore returns a shallow copy of the document with the given score.
func (d Document) WithScore(score float32) Document {
	d.Score = score
	return d
}

// WithMetadata returns a copy of the document with the given metadata key-value
// pair added or updated. The underlying Metadata map is cloned to ensure
// caller isolation.
func (d Document) WithMetadata(key string, value any) Document {
	cloned := CloneMetadata(d.Metadata)
	if cloned == nil {
		cloned = make(map[string]any)
	}
	cloned[key] = value
	d.Metadata = cloned
	return d
}

// CloneMetadata creates a shallow copy of the provided metadata map.
// If m is nil, CloneMetadata returns nil.
func CloneMetadata(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	clone := make(map[string]any, len(m))
	for k, v := range m {
		clone[k] = v
	}
	return clone
}
