package store

// PutOption configures the behavior of [Store.Put].
type PutOption func(*putConfig)

// GetOption configures the behavior of [Store.Get].
type GetOption func(*getConfig)

// SearchOption configures the behavior of [Store.Search].
type SearchOption func(*SearchConfig)

// DeleteOption configures the behavior of [Store.Delete].
type DeleteOption func(*deleteConfig)

// putConfig holds optional parameters for Put operations.
type putConfig struct{}

// getConfig holds optional parameters for Get operations.
type getConfig struct{}

// SearchConfig holds optional parameters for Search operations.
type SearchConfig struct {
	Prefix string
	Limit  int
	HasLim bool
	Offset int
	HasOff bool
}

// deleteConfig holds optional parameters for Delete operations.
type deleteConfig struct{}

// WithKeyPrefix filters Search results to keys matching the given prefix.
func WithKeyPrefix(prefix string) SearchOption {
	return func(sc *SearchConfig) { sc.Prefix = prefix }
}

// WithLimit caps the number of items returned by Search.
func WithLimit(n int) SearchOption {
	return func(sc *SearchConfig) { sc.Limit = n; sc.HasLim = true }
}

// WithOffset skips the first N items returned by Search (pagination).
func WithOffset(n int) SearchOption {
	return func(sc *SearchConfig) { sc.Offset = n; sc.HasOff = true }
}
