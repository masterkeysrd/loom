package collection

import (
	"reflect"
	"testing"
)

func TestMergeMap(t *testing.T) {
	t.Run("nil src returns dst unchanged", func(t *testing.T) {
		dst := map[string]int{"a": 1}
		got := MergeMap(dst, nil)
		if !reflect.DeepEqual(got, dst) {
			t.Errorf("got %v, want %v", got, dst)
		}
	})

	t.Run("empty src returns dst unchanged", func(t *testing.T) {
		dst := map[string]int{"a": 1}
		got := MergeMap(dst, map[string]int{})
		if !reflect.DeepEqual(got, dst) {
			t.Errorf("got %v, want %v", got, dst)
		}
	})

	t.Run("nil dst with non-empty src returns clone of src", func(t *testing.T) {
		src := map[string]int{"a": 1, "b": 2}
		got := MergeMap(nil, src)
		if !reflect.DeepEqual(got, src) {
			t.Errorf("got %v, want %v", got, src)
		}
		// Verify deep copy / clone isolation
		got["a"] = 99
		if src["a"] != 1 {
			t.Errorf("src was mutated: src[a]=%d", src["a"])
		}
	})

	t.Run("nil dst with empty src returns nil", func(t *testing.T) {
		var dst map[string]int
		got := MergeMap(dst, map[string]int{})
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("merge non-empty into non-empty", func(t *testing.T) {
		dst := map[string]string{"a": "1", "b": "2"}
		src := map[string]string{"c": "3", "d": "4"}
		got := MergeMap(dst, src)
		want := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("key overwrites", func(t *testing.T) {
		dst := map[string]string{"a": "old", "b": "keep"}
		src := map[string]string{"a": "new", "c": "add"}
		got := MergeMap(dst, src)
		want := map[string]string{"a": "new", "b": "keep", "c": "add"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}
