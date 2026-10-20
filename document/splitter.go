package document

import (
	"fmt"
	"iter"
	"unicode/utf8"
)

// Splitter divides text or documents into smaller segments.
type Splitter interface {
	SplitText(text string) []string
	SplitDocuments(docs []Document) []Document
}

// LengthFunc calculates the length of a string (e.g. rune count, word count, token count).
type LengthFunc func(string) int

// SplitterConfig holds configuration options for text splitters.
type SplitterConfig struct {
	ChunkSize      int
	ChunkOverlap   int
	Separators     []string
	LengthFunc     LengthFunc
	KeepSeparator  bool
	TrimWhitespace bool
}

// SplitterOption configures a SplitterConfig.
type SplitterOption func(*SplitterConfig)

// WithChunkSize sets the maximum size of each chunk (default: 1000).
func WithChunkSize(size int) SplitterOption {
	return func(c *SplitterConfig) {
		c.ChunkSize = size
	}
}

// WithChunkOverlap sets the target overlap between consecutive chunks (default: 200).
func WithChunkOverlap(overlap int) SplitterOption {
	return func(c *SplitterConfig) {
		c.ChunkOverlap = overlap
	}
}

// WithSeparators sets the separators to use for splitting.
func WithSeparators(seps ...string) SplitterOption {
	return func(c *SplitterConfig) {
		c.Separators = seps
	}
}

// WithLengthFunc sets a custom length calculation function (default: utf8.RuneCountInString).
func WithLengthFunc(fn LengthFunc) SplitterOption {
	return func(c *SplitterConfig) {
		c.LengthFunc = fn
	}
}

// WithKeepSeparator sets whether to keep the separator in chunks (default: false).
func WithKeepSeparator(keep bool) SplitterOption {
	return func(c *SplitterConfig) {
		c.KeepSeparator = keep
	}
}

// WithTrimWhitespace sets whether to trim whitespace from each chunk (default: true).
func WithTrimWhitespace(trim bool) SplitterOption {
	return func(c *SplitterConfig) {
		c.TrimWhitespace = trim
	}
}

func newSplitterConfig(opts ...SplitterOption) SplitterConfig {
	cfg := SplitterConfig{
		ChunkSize:      1000,
		ChunkOverlap:   200,
		Separators:     []string{"\n\n", "\n", " ", ""},
		LengthFunc:     utf8.RuneCountInString,
		KeepSeparator:  false,
		TrimWhitespace: true,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if len(cfg.Separators) == 0 {
		cfg.Separators = []string{"\n\n", "\n", " ", ""}
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 1000
	}
	if cfg.ChunkOverlap < 0 {
		cfg.ChunkOverlap = 0
	}
	if cfg.ChunkOverlap >= cfg.ChunkSize {
		cfg.ChunkOverlap = cfg.ChunkSize - 1
	}
	if cfg.LengthFunc == nil {
		cfg.LengthFunc = utf8.RuneCountInString
	}
	return cfg
}

// EnrichChunkMetadata produces a slice of Document chunks from raw chunk strings,
// copying the parent document's Metadata and setting "chunk_index", "chunk_total",
// and if doc.ID != "", "parent_id" and ID: fmt.Sprintf("%s#%d", doc.ID, i).
func EnrichChunkMetadata(doc Document, chunks []string) []Document {
	if len(chunks) == 0 {
		return nil
	}
	total := len(chunks)
	out := make([]Document, total)
	for i, chunk := range chunks {
		meta := CloneMetadata(doc.Metadata)
		if meta == nil {
			meta = make(map[string]any)
		}
		meta["chunk_index"] = i
		meta["chunk_total"] = total
		if doc.ID != "" {
			meta["parent_id"] = doc.ID
		}
		chunkDoc := Document{
			Content:  chunk,
			Metadata: meta,
			Score:    doc.Score,
		}
		if doc.ID != "" {
			chunkDoc.ID = fmt.Sprintf("%s#%d", doc.ID, i)
		}
		out[i] = chunkDoc
	}
	return out
}

// SplitIter wraps an iterator of Document items and lazily yields split chunks
// using the provided Splitter. If seq yields an error, SplitIter yields that
// error and stops immediately.
func SplitIter(seq iter.Seq2[Document, error], s Splitter) iter.Seq2[Document, error] {
	return func(yield func(Document, error) bool) {
		for doc, err := range seq {
			if err != nil {
				yield(Document{}, err)
				return
			}
			chunks := s.SplitDocuments([]Document{doc})
			for _, chunk := range chunks {
				if !yield(chunk, nil) {
					return
				}
			}
		}
	}
}
