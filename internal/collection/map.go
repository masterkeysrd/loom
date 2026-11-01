package collection

import "maps"

// MergeMap merges key-value pairs from src into dst. If src is empty, dst is
// returned unchanged. If dst is nil, a clone of src is returned.
func MergeMap[M ~map[K]V, K comparable, V any](dst, src M) M {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		return maps.Clone(src)
	}
	maps.Copy(dst, src)
	return dst
}
