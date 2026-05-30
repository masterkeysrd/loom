package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ErrCheckpointNotFound is returned by [Checkpointer.Load] when no checkpoint
// exists for the requested [Location].
var ErrCheckpointNotFound = fmt.Errorf("checkpoint not found")

// Checkpointer is the persistence interface for graph execution state.
// Implementations are responsible for durable storage of each checkpoint so
// that a graph can be resumed after a process restart or an interrupt.
type Checkpointer interface {
	// Load retrieves the checkpoint identified by the given Location.
	// It returns nil, nil when no matching checkpoint is found.
	Load(context.Context, Location) (*Checkpoint, error)

	// Record persists a checkpoint created during graph execution.
	Record(context.Context, Checkpoint) error
}

// Checkpoint is the serializable form of a [Snapshot], written to and read
// from a [Checkpointer]. State is stored as raw JSON so that the checkpointer
// implementation remains decoupled from the concrete State type.
type Checkpoint struct {
	Location  Location        `json:"location"`
	Parent    *Location       `json:"parent,omitempty"`
	State     any             `json:"state"`
	Next      []string        `json:"next"`
	Timestamp time.Time       `json:"timestamp"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}
