package collection

import (
	"fmt"
	"iter"
	"strings"
)

// OrderedMap maintains the order of keys based on their first insertion.
type OrderedMap[K comparable, V any] struct {
	keys   []K
	values map[K]V
}

// NewOrderedMap creates a new instance of OrderedMap.
func NewOrderedMap[K comparable, V any]() *OrderedMap[K, V] {
	return &OrderedMap[K, V]{
		keys:   []K{},
		values: make(map[K]V),
	}
}

// Set adds or updates a key-value pair.
// If the key already exists, its value is updated but its position in the order is preserved.
func (om *OrderedMap[K, V]) Set(key K, value V) {
	if _, exists := om.values[key]; !exists {
		om.keys = append(om.keys, key)
	}
	om.values[key] = value
}

// Get retrieves a value and a boolean indicating if the key was found.
func (om *OrderedMap[K, V]) Get(key K) (V, bool) {
	val, ok := om.values[key]
	return val, ok
}

// Delete removes a key from the map.
// Note: This is an O(n) operation due to the slice removal.
func (om *OrderedMap[K, V]) Delete(key K) {
	if _, exists := om.values[key]; !exists {
		return
	}

	delete(om.values, key)

	// Remove from keys slice
	for i, k := range om.keys {
		if k == key {
			om.keys = append(om.keys[:i], om.keys[i+1:]...)
			break
		}
	}
}

// Len returns the number of elements in the map.
func (om *OrderedMap[K, V]) Len() int {
	return len(om.values)
}

// Keys returns the keys in their insertion order.
func (om *OrderedMap[K, V]) Keys() []K {
	// Return a copy to prevent external mutation
	cp := make([]K, len(om.keys))
	copy(cp, om.keys)
	return cp
}

// ForEach iterates over the map in order and calls the provided function.
func (om *OrderedMap[K, V]) ForEach(fn func(key K, value V) error) error {
	for _, k := range om.keys {
		if err := fn(k, om.values[k]); err != nil {
			return err
		}
	}
	return nil
}

// Entries returns an iterator over the key-value pairs in order.
func (om *OrderedMap[K, V]) Entries() iter.Seq2[K, V] {
	return iter.Seq2[K, V](func(yield func(K, V) bool) {
		for _, k := range om.keys {
			if !yield(k, om.values[k]) {
				return
			}
		}
	})
}

// Values returns the values in the order of their keys.
func (om *OrderedMap[K, V]) Values() iter.Seq[V] {
	return iter.Seq[V](func(yield func(V) bool) {
		for _, k := range om.keys {
			if !yield(om.values[k]) {
				return
			}
		}
	})
}

// String provides a string representation of the map for debugging.
func (om *OrderedMap[K, V]) String() string {
	var s strings.Builder
	s.WriteString("{")
	for i, k := range om.keys {
		fmt.Fprintf(&s, "%v: %v", k, om.values[k])
		if i < len(om.keys)-1 {
			s.WriteString(", ")
		}
	}
	s.WriteString("}")
	return s.String()
}
