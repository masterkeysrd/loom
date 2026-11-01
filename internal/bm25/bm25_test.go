package bm25

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultTokenizer(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{
			input: "get_weather_forecast",
			want:  []string{"get", "weather", "forecast"},
		},
		{
			input: "Quick brown FOX jumps 42 times!",
			want:  []string{"quick", "brown", "fox", "jumps", "42", "times"},
		},
		{
			input: "   ",
			want:  nil,
		},
		{
			input: "apple-fruit,banana_pie.orange",
			want:  []string{"apple", "fruit", "banana", "pie", "orange"},
		},
	}

	for _, tc := range tests {
		got := DefaultTokenizer(tc.input)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("DefaultTokenizer(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestBM25SearchAndSort(t *testing.T) {
	idx := New[string](
		WithK1[string](1.2),
		WithB[string](0.75),
	)

	idx.Add("the quick brown fox jumps over the lazy dog", "doc-1")
	idx.Add("quick brown rabbits are very energetic and jump high", "doc-2")
	idx.Add("golang standard library documentation and concurrency patterns", "doc-3")

	// 1. Search for fox
	results := idx.SearchSorted("quick brown fox", nil)
	if len(results) == 0 {
		t.Fatalf("expected results, got 0")
	}
	if results[0].Item != "doc-1" {
		t.Errorf("expected doc-1 as top result, got %s", results[0].Item)
	}
	if results[0].Score <= 0 {
		t.Errorf("expected positive score, got %f", results[0].Score)
	}

	// 2. Filter test
	filterRes := idx.Search("concurrency patterns", func(item string) bool {
		return item == "doc-3"
	})
	if len(filterRes) != 1 || filterRes[0].Item != "doc-3" {
		t.Fatalf("expected doc-3 matching filter, got %v", filterRes)
	}

	// 3. No match
	noMatch := idx.Search("unrelatedtermxyz", nil)
	if len(noMatch) != 0 {
		t.Errorf("expected 0 matches, got %d", len(noMatch))
	}

	// 4. Empty query
	emptyQuery := idx.Search("", nil)
	if len(emptyQuery) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(emptyQuery))
	}
}

func TestBM25Clone(t *testing.T) {
	type itemType struct {
		Name string
		Tags []string
	}

	idx := New[*itemType]()
	idx.Add("apple orange fruit", &itemType{Name: "item1", Tags: []string{"fruit"}})
	idx.Add("carrot celery vegetable", &itemType{Name: "item2", Tags: []string{"veggie"}})

	cloned := idx.Clone(func(item *itemType) *itemType {
		tags := make([]string, len(item.Tags))
		copy(tags, item.Tags)
		return &itemType{
			Name: item.Name,
			Tags: tags,
		}
	})

	// Add new item to original
	idx.Add("banana fruit", &itemType{Name: "item3", Tags: []string{"fruit"}})

	resOrig := idx.Search("banana", nil)
	if len(resOrig) != 1 {
		t.Fatalf("expected 1 match in original, got %d", len(resOrig))
	}

	resCloned := cloned.Search("banana", nil)
	if len(resCloned) != 0 {
		t.Fatalf("expected 0 matches in cloned, got %d", len(resCloned))
	}
}

func TestBM25CustomTokenizer(t *testing.T) {
	customTok := func(s string) []string {
		return strings.Fields(strings.ToUpper(s))
	}

	idx := New[string](WithTokenizer[string](customTok))
	idx.Add("ALPHA BETA GAMMA", "doc-1")
	idx.Add("DELTA EPSILON", "doc-2")

	res := idx.Search("alpha", nil)
	if len(res) != 1 || res[0].Item != "doc-1" {
		t.Fatalf("expected doc-1 matching custom tokenizer, got %v", res)
	}
}
