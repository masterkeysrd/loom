package graph

import (
	"bytes"
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

// NoopCheckpointer is the default [Checkpointer] used when no persistent
// checkpointer is configured. Load always reports no checkpoint found and
// Record is a no-op.
type NoopCheckpointer struct{}

// Load implements [Checkpointer] and always returns nil, nil.
func (NoopCheckpointer) Load(context.Context, Location) (*Checkpoint, error) {
	return nil, nil
}

// Record implements [Checkpointer] and is a no-op.
func (NoopCheckpointer) Record(context.Context, Checkpoint) error {
	return nil
}

// WriteOp describes how a channel write is applied during checkpoint state reconstruction.
type WriteOp string

const (
	// WriteOpSet replaces the channel's value with a new full seed value.
	WriteOpSet WriteOp = "set"
	// WriteOpAppend appends a JSON array delta to the channel's prior JSON array value.
	WriteOpAppend WriteOp = "append"
)

// ChannelWrite represents a single channel mutation recorded in a checkpoint step.
type ChannelWrite struct {
	Op    WriteOp `json:"op"`
	Value []byte  `json:"value"`
}

// MarshalJSON marshals ChannelWrite ensuring Value is emitted as raw JSON.
func (cw ChannelWrite) MarshalJSON() ([]byte, error) {
	type rawCW struct {
		Op    WriteOp         `json:"op"`
		Value json.RawMessage `json:"value"`
	}
	val := json.RawMessage("null")
	if len(cw.Value) > 0 {
		val = json.RawMessage(cw.Value)
	}
	return json.Marshal(rawCW{
		Op:    cw.Op,
		Value: val,
	})
}

// UnmarshalJSON unmarshals ChannelWrite preserving raw JSON in Value.
func (cw *ChannelWrite) UnmarshalJSON(data []byte) error {
	type rawCW struct {
		Op    WriteOp         `json:"op"`
		Value json.RawMessage `json:"value"`
	}
	var r rawCW
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	cw.Op = r.Op
	cw.Value = []byte(r.Value)
	return nil
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
	Location      Location                `json:"location"`
	Parent        *Location               `json:"parent,omitempty"`
	State         StateMap                `json:"state"`
	ChannelWrites map[string]ChannelWrite `json:"channel_writes,omitempty"`
	Next          []string                `json:"next"`
	Timestamp     time.Time               `json:"timestamp"`
	Metadata      json.RawMessage         `json:"metadata,omitempty"`
}

// CheckpointMetadata holds execution metadata for a checkpoint.
type CheckpointMetadata struct {
	Node   string         `json:"node,omitempty"`
	Source string         `json:"source"`
	Writes map[string]any `json:"writes"`
	Step   int            `json:"step"`
}

// DiffState computes the channel-level difference between before and after states.
// When before is empty, all channels in after are recorded as WriteOpSet seeds.
// For array channels where after is an append to before, WriteOpAppend is used with
// only the new delta elements.
func DiffState(before, after StateMap) map[string]ChannelWrite {
	writes := make(map[string]ChannelWrite)
	if len(after) == 0 {
		return writes
	}
	if len(before) == 0 {
		for k, v := range after {
			writes[k] = ChannelWrite{
				Op:    WriteOpSet,
				Value: v,
			}
		}
		return writes
	}

	for k, afterData := range after {
		beforeData, exists := before[k]
		if exists && bytes.Equal(beforeData, afterData) {
			continue
		}

		if exists {
			if delta, ok := extractJSONArrayAppend(beforeData, afterData); ok {
				writes[k] = ChannelWrite{
					Op:    WriteOpAppend,
					Value: delta,
				}
				continue
			}
		}

		writes[k] = ChannelWrite{
			Op:    WriteOpSet,
			Value: afterData,
		}
	}
	return writes
}

func extractJSONArrayAppend(beforeData, afterData []byte) ([]byte, bool) {
	b := bytes.TrimSpace(beforeData)
	a := bytes.TrimSpace(afterData)

	if len(b) <= 2 || b[0] != '[' || b[len(b)-1] != ']' {
		return nil, false
	}
	if len(a) <= len(b)+1 || a[0] != '[' || a[len(a)-1] != ']' {
		return nil, false
	}
	if !bytes.HasPrefix(a, b[:len(b)-1]) || a[len(b)-1] != ',' {
		return nil, false
	}
	appended := a[len(b) : len(a)-1]
	if len(bytes.TrimSpace(appended)) == 0 {
		return nil, false
	}

	delta := make([]byte, 0, len(a)-len(b)+1)
	delta = append(delta, '[')
	delta = append(delta, a[len(b):]...)
	return delta, true
}

// ApplyChannelWrite applies a single channel mutation to the current channel data.
// If w.Op is WriteOpAppend, the JSON array delta is stitched onto current.
// Otherwise, a copy of w.Value replaces current.
func ApplyChannelWrite(current []byte, w ChannelWrite) ([]byte, error) {
	if w.Op != WriteOpAppend || len(current) == 0 {
		valCopy := make([]byte, len(w.Value))
		copy(valCopy, w.Value)
		return valCopy, nil
	}

	curr := bytes.TrimSpace(current)
	val := bytes.TrimSpace(w.Value)

	if bytes.Equal(val, []byte("[]")) {
		valCopy := make([]byte, len(current))
		copy(valCopy, current)
		return valCopy, nil
	}
	if len(val) <= 2 || val[0] != '[' || val[len(val)-1] != ']' {
		return nil, fmt.Errorf("invalid append delta JSON array: %s", string(w.Value))
	}
	if bytes.Equal(curr, []byte("[]")) {
		valCopy := make([]byte, len(val))
		copy(valCopy, val)
		return valCopy, nil
	}
	if len(curr) < 2 || curr[0] != '[' || curr[len(curr)-1] != ']' {
		return nil, fmt.Errorf("cannot append delta to non-array JSON: %s", string(current))
	}

	out := make([]byte, 0, len(curr)+len(val)-1)
	out = append(out, curr[:len(curr)-1]...)
	out = append(out, ',')
	out = append(out, val[1:]...)
	return out, nil
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
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
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
