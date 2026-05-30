package llm

import (
	"fmt"
	"sync"
)

// Registry is a thread-safe store of named [Provider] implementations.
// It allows different parts of the application to look up a provider by name
// without creating hard dependencies on specific backend packages.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry returns an empty [Registry] ready for use.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register stores provider under name, overwriting any previously registered
// provider with the same name. It is safe to call from multiple goroutines.
func (r *Registry) Register(name string, provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = provider
}

// Get retrieves the [Provider] registered under name.
// It returns an error if no provider with that name exists.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("llm: provider %q not found in registry", name)
	}

	return provider, nil
}
