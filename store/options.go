package store

// PutOption configures the behavior of [Store.Put].
type PutOption func(*PutConfig)

// GetOption configures the behavior of [Store.Get].
type GetOption func(*getConfig)

// SearchOption configures the behavior of [Store.Search].
type SearchOption func(*SearchConfig)

// DeleteOption configures the behavior of [Store.Delete].
type DeleteOption func(*deleteConfig)

// PutConfig holds optional parameters for Put operations.
type PutConfig struct {
	DisableIndex   bool
	FieldsOverride []string
}

// getConfig holds optional parameters for Get operations.
type getConfig struct{}

// SearchConfig holds optional parameters for Search operations.
type SearchConfig struct {
	Prefix string
	Query  string
	Limit  int
	HasLim bool
	Offset int
	HasOff bool
}

// deleteConfig holds optional parameters for Delete operations.
type deleteConfig struct{}

// WithIndexDisabled skips vector embedding generation for a single [Store.Put] call.
func WithIndexDisabled() PutOption {
	return func(pc *PutConfig) { pc.DisableIndex = true }
}

// WithIndexFields overrides the store's default [IndexConfig.Fields] for a single [Store.Put] call.
func WithIndexFields(fields ...string) PutOption {
	return func(pc *PutConfig) { pc.FieldsOverride = fields }
}

// WithKeyPrefix filters Search results to keys matching the given prefix.
func WithKeyPrefix(prefix string) SearchOption {
	return func(sc *SearchConfig) { sc.Prefix = prefix }
}

// WithQuery performs a semantic similarity search using the store's configured [IndexConfig].
// Results are ordered by similarity to the query (most similar first).
func WithQuery(query string) SearchOption {
	return func(sc *SearchConfig) { sc.Query = query }
}

// WithLimit caps the number of items returned by Search.
func WithLimit(n int) SearchOption {
	return func(sc *SearchConfig) { sc.Limit = n; sc.HasLim = true }
}

// WithOffset skips the first N items returned by Search (pagination).
func WithOffset(n int) SearchOption {
	return func(sc *SearchConfig) { sc.Offset = n; sc.HasOff = true }
}
