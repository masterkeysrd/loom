package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
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

// StateMap is a map of channel names to JSON bytes.
type StateMap map[string][]byte

// MarshalJSON marshals StateMap into a JSON object of raw JSON fields rather than base64 strings.
func (sm StateMap) MarshalJSON() ([]byte, error) {
	if sm == nil {
		return []byte("null"), nil
	}
	raw := make(map[string]json.RawMessage, len(sm))
	for k, v := range sm {
		raw[k] = json.RawMessage(v)
	}
	return json.Marshal(raw)
}

// UnmarshalJSON unmarshals a JSON object of raw JSON fields into StateMap.
func (sm *StateMap) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*sm = make(StateMap, len(raw))
	for k, v := range raw {
		(*sm)[k] = []byte(v)
	}
	return nil
}

// Checkpoint is the serializable form of a [Snapshot], written to and read
// from a [Checkpointer]. State is stored as raw JSON so that the checkpointer
// implementation remains decoupled from the concrete State type.
type Checkpoint struct {
	Location  Location        `json:"location"`
	Parent    *Location       `json:"parent,omitempty"`
	State     StateMap        `json:"state"`
	Next      []string        `json:"next"`
	Timestamp time.Time       `json:"timestamp"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
}

// CheckpointMetadata holds execution metadata for a checkpoint.
type CheckpointMetadata struct {
	Node   string         `json:"node,omitempty"`
	Source string         `json:"source"`
	Writes map[string]any `json:"writes"`
	Step   int            `json:"step"`
}

// DecomposeState decomposes the state into a map of channel names to JSON bytes.
// Each exported field in the state struct represents a channel. If the state is not a struct,
// it falls back to a single "__root__" channel.
func DecomposeState(state any) (StateMap, error) {
	if state == nil {
		return nil, nil
	}

	// Try to resolve pointers/interfaces to the underlying concrete value.
	v := reflect.ValueOf(state)
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		// Fallback to single "__root__" channel if not a struct
		data, err := json.Marshal(state)
		if err != nil {
			return nil, err
		}
		return StateMap{"__root__": data}, nil
	}

	channels := make(StateMap)
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // skip unexported fields
		}

		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" && tag != "-" {
			parts := strings.Split(tag, ",")
			if parts[0] != "" {
				name = parts[0]
			}
		}

		val := v.Field(i).Interface()
		data, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("marshal field %q: %w", field.Name, err)
		}
		channels[name] = data
	}

	return channels, nil
}
