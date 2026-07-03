package store

import "context"

// Get is a generic convenience wrapper around [Store.Get].
// It returns a typed value directly instead of requiring the caller
// to pass a pointer.
func Get[T any](ctx context.Context, s Store, namespace, key string, opts ...GetOption) (T, error) {
	var item T
	err := s.Get(ctx, namespace, key, &item, opts...)
	return item, err
}

// Search is a generic convenience wrapper around [Store.Search].
// It returns a typed slice directly instead of requiring the caller
// to pass a pointer to a slice.
func Search[T any](ctx context.Context, s Store, namespace string, opts ...SearchOption) ([]T, error) {
	var items []T
	err := s.Search(ctx, namespace, &items, opts...)
	return items, err
}
