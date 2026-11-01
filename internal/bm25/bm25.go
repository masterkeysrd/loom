package bm25

import (
	"maps"
	"math"
	"slices"
	"strings"
	"unicode"
)

// TokenizerFunc tokenizes a text string into a sequence of terms.
type TokenizerFunc func(string) []string

// DefaultTokenizer provides a Unicode-aware lowercase letter/digit word tokenizer
// that splits on any non-alphanumeric characters (including underscores and punctuation).
func DefaultTokenizer(text string) []string {
	var tokens []string
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			if b.Len() > 0 {
				tokens = append(tokens, b.String())
				b.Reset()
			}
		}
	}
	if b.Len() > 0 {
		tokens = append(tokens, b.String())
	}
	return tokens
}

// Result represents a scored search result.
type Result[T any] struct {
	Item  T
	Score float64
	Index int
}

// Option configures an [Index].
type Option[T any] func(*Index[T])

// WithK1 sets the term frequency saturation parameter k1 (default: 1.5).
func WithK1[T any](k1 float64) Option[T] {
	return func(idx *Index[T]) {
		if k1 >= 0 {
			idx.k1 = k1
		}
	}
}

// WithB sets the document length normalization parameter b (default: 0.75).
func WithB[T any](b float64) Option[T] {
	return func(idx *Index[T]) {
		if b >= 0 && b <= 1 {
			idx.b = b
		}
	}
}

// WithTokenizer sets a custom tokenizer function.
func WithTokenizer[T any](fn TokenizerFunc) Option[T] {
	return func(idx *Index[T]) {
		if fn != nil {
			idx.tokenizer = fn
		}
	}
}

// Index implements the in-memory Okapi BM25 ranking algorithm for arbitrary items.
type Index[T any] struct {
	k1        float64
	b         float64
	tokenizer TokenizerFunc

	items      []T
	docLengths []int
	docTF      []map[string]int
	df         map[string]int
	totalLen   int
}

// New creates an empty [Index] configured with the provided options.
func New[T any](opts ...Option[T]) *Index[T] {
	idx := &Index[T]{
		k1:        1.5,
		b:         0.75,
		tokenizer: DefaultTokenizer,
		df:        make(map[string]int),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(idx)
		}
	}
	return idx
}

// Add indexes an item by tokenizing the provided text.
func (idx *Index[T]) Add(text string, item T) {
	tokens := idx.tokenizer(text)
	docLen := len(tokens)
	idx.totalLen += docLen
	idx.docLengths = append(idx.docLengths, docLen)

	tf := make(map[string]int, len(tokens))
	for _, tok := range tokens {
		tf[tok]++
	}
	for term := range tf {
		idx.df[term]++
	}
	idx.docTF = append(idx.docTF, tf)
	idx.items = append(idx.items, item)
}

// Search scores all indexed items matching filter (if non-nil) against query using Okapi BM25,
// returning matches with score > 0 in their indexed order.
func (idx *Index[T]) Search(query string, filter func(T) bool) []Result[T] {
	N := len(idx.items)
	if N == 0 {
		return nil
	}

	tokens := idx.tokenizer(query)
	if len(tokens) == 0 {
		return nil
	}

	uniqueTerms := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		uniqueTerms[tok] = struct{}{}
	}

	avgdl := float64(idx.totalLen) / float64(N)
	if avgdl == 0 {
		avgdl = 1.0
	}

	idfs := make(map[string]float64, len(uniqueTerms))
	for term := range uniqueTerms {
		dfVal := float64(idx.df[term])
		if dfVal > 0 {
			idf := math.Log(1.0 + (float64(N)-dfVal+0.5)/(dfVal+0.5))
			idfs[term] = idf
		}
	}

	var results []Result[T]
	for i, item := range idx.items {
		if filter != nil && !filter(item) {
			continue
		}
		tfMap := idx.docTF[i]
		docLen := float64(idx.docLengths[i])
		lenNorm := 1.0 - idx.b + idx.b*(docLen/avgdl)

		var score float64
		for term, idf := range idfs {
			tf := float64(tfMap[term])
			if tf <= 0 {
				continue
			}
			score += idf * (tf * (idx.k1 + 1.0)) / (tf + idx.k1*lenNorm)
		}
		if score > 0 {
			results = append(results, Result[T]{
				Item:  item,
				Score: score,
				Index: i,
			})
		}
	}
	return results
}

// SearchSorted performs a search and sorts results by score descending, breaking ties by index ascending.
func (idx *Index[T]) SearchSorted(query string, filter func(T) bool) []Result[T] {
	results := idx.Search(query, filter)
	SortResults(results)
	return results
}

// SortResults sorts results by score in descending order, breaking ties by index ascending.
func SortResults[T any](results []Result[T]) {
	slices.SortFunc(results, func(a, b Result[T]) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		return a.Index - b.Index
	})
}

// Clone creates a deep copy of the Index. If cloneItem is non-nil, it is called to clone each item.
func (idx *Index[T]) Clone(cloneItem func(T) T) *Index[T] {
	if idx == nil {
		return nil
	}
	clone := &Index[T]{
		k1:         idx.k1,
		b:          idx.b,
		tokenizer:  idx.tokenizer,
		docLengths: slices.Clone(idx.docLengths),
		df:         maps.Clone(idx.df),
		totalLen:   idx.totalLen,
	}
	if idx.items != nil {
		clone.items = make([]T, len(idx.items))
		for i, item := range idx.items {
			if cloneItem != nil {
				clone.items[i] = cloneItem(item)
			} else {
				clone.items[i] = item
			}
		}
	}
	if idx.docTF != nil {
		clone.docTF = make([]map[string]int, len(idx.docTF))
		for i, tf := range idx.docTF {
			clone.docTF[i] = maps.Clone(tf)
		}
	}
	return clone
}
