package llm

import (
	"fmt"
	"sync"
)

// ProviderFactory is a function that creates a [Provider] on demand.
type ProviderFactory func() (Provider, error)

// Registry is a thread-safe store of [Provider] factories.
// It allows different parts of the application to look up a provider by name
// without creating hard dependencies on specific backend packages,
// and ensures providers are only instantiated when needed.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]*registryEntry
}

type registryEntry struct {
	factory ProviderFactory
	once    sync.Once
	p       Provider
	err     error
}

// NewRegistry returns an empty [Registry] ready for use.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]*registryEntry),
	}
}

// Register stores a provider factory under name, overwriting any previously
// registered factory with the same name. It is safe to call from multiple goroutines.
func (r *Registry) Register(name string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = &registryEntry{
		factory: factory,
	}
}

// Get retrieves the [Provider] registered under name.
// It calls the registered factory function if the provider hasn't been
// created yet. It returns an error if no factory is registered for name
// or if the factory returns an error.
func (r *Registry) Get(name string) (Provider, error) {
	r.mu.RLock()
	entry, ok := r.providers[name]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("llm: provider %q not found in registry", name)
	}

	entry.once.Do(func() {
		entry.p, entry.err = entry.factory()
	})

	if entry.err != nil {
		return nil, fmt.Errorf("llm: failed to initialize provider %q: %w", name, entry.err)
	}

	return entry.p, nil
}
