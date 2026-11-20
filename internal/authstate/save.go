// Package authstate provides persistence helpers for credential sources.
package authstate

// PendingSave retains a credential when its write fails. Call Retry before
// obtaining another credential. The caller serializes access to both methods.
type PendingSave[T any] struct {
	Write   func(T) error
	pending *T
}

// Save writes value when a persistence callback is configured.
func (s *PendingSave[T]) Save(value T) error {
	if s.Write == nil {
		return nil
	}
	if err := s.Write(value); err != nil {
		s.pending = &value
		return err
	}
	s.pending = nil
	return nil
}

// Retry repeats a failed write, if any.
func (s *PendingSave[T]) Retry() error {
	if s.pending == nil {
		return nil
	}
	return s.Save(*s.pending)
}
