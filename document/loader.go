package document

import (
	"context"
	"errors"
	"iter"
)

// Loader defines an abstraction for lazily loading documents from a data source.
type Loader interface {
	Load(ctx context.Context) iter.Seq2[Document, error]
}

// LoaderFunc is an adapter function that implements the Loader interface.
type LoaderFunc func(ctx context.Context) iter.Seq2[Document, error]

// Load calls f(ctx).
func (f LoaderFunc) Load(ctx context.Context) iter.Seq2[Document, error] {
	return f(ctx)
}

// LoadAll exhausts the loader iterator and collects all documents into a slice.
// If the loader yields an error or ctx is cancelled, LoadAll returns the
// documents collected so far alongside the error.
func LoadAll(ctx context.Context, l Loader) ([]Document, error) {
	if l == nil {
		return nil, errors.New("document: nil loader")
	}
	var docs []Document
	for doc, err := range l.Load(ctx) {
		if err != nil {
			return docs, err
		}
		if err := ctx.Err(); err != nil {
			return docs, err
		}
		docs = append(docs, doc)
	}
	if err := ctx.Err(); err != nil {
		return docs, err
	}
	return docs, nil
}
