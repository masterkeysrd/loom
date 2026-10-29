package graph

import (
	"context"
	"errors"
	"slices"
	"time"
)

// Middleware wraps a Node to intercept or modify its execution.
type Middleware[S any] func(Node[S]) Node[S]

// ChainMiddleware applies middlewares in order (first middleware is outermost), skipping nil middlewares.
func ChainMiddleware[S any](node Node[S], mws ...Middleware[S]) Node[S] {
	for _, mw := range slices.Backward(mws) {
		if mw != nil {
			node = mw(node)
		}
	}
	return node
}

// RetryConfig defines the configuration for retry behavior.
type RetryConfig struct {
	MaxAttempts int              // Total attempts including initial call (default: 3)
	InitialWait time.Duration    // Initial backoff duration (default: 100ms)
	MaxWait     time.Duration    // Maximum backoff duration (default: 5s)
	Multiplier  float64          // Backoff multiplier (default: 2.0)
	ShouldRetry func(error) bool // Optional predicate; if nil, retries all non-context errors
}

// RetryOption configures a RetryConfig.
type RetryOption func(*RetryConfig)

// WithMaxAttempts sets the total number of attempts including the initial call.
func WithMaxAttempts(n int) RetryOption {
	return func(c *RetryConfig) {
		c.MaxAttempts = n
	}
}

// WithInitialWait sets the initial backoff duration between retry attempts.
func WithInitialWait(d time.Duration) RetryOption {
	return func(c *RetryConfig) {
		c.InitialWait = d
	}
}

// WithMaxWait sets the maximum backoff duration between retry attempts.
func WithMaxWait(d time.Duration) RetryOption {
	return func(c *RetryConfig) {
		c.MaxWait = d
	}
}

// WithMultiplier sets the backoff duration multiplier between retry attempts.
func WithMultiplier(m float64) RetryOption {
	return func(c *RetryConfig) {
		c.Multiplier = m
	}
}

// WithShouldRetry sets a custom predicate to decide whether an error should trigger a retry.
func WithShouldRetry(fn func(error) bool) RetryOption {
	return func(c *RetryConfig) {
		c.ShouldRetry = fn
	}
}

// RetryMiddleware returns a Middleware that retries failed node executions according to the options.
func RetryMiddleware[S State[S]](opts ...RetryOption) Middleware[S] {
	cfg := RetryConfig{
		MaxAttempts: 3,
		InitialWait: 100 * time.Millisecond,
		MaxWait:     5 * time.Second,
		Multiplier:  2.0,
		ShouldRetry: nil,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}

	return func(next Node[S]) Node[S] {
		return NodeFunc(func(ctx context.Context, state S) (Command[S], error) {
			wait := cfg.InitialWait
			for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}

				cmd, err := next.Execute(ctx, state.Copy())
				if err == nil {
					return cmd, nil
				}

				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (cfg.ShouldRetry != nil && !cfg.ShouldRetry(err)) {
					return nil, err
				}

				if attempt == cfg.MaxAttempts {
					return nil, err
				}

				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}

				if cfg.Multiplier > 0 {
					wait = time.Duration(float64(wait) * cfg.Multiplier)
				}
				if cfg.MaxWait > 0 && wait > cfg.MaxWait {
					wait = cfg.MaxWait
				}
			}
			return nil, nil
		})
	}
}
