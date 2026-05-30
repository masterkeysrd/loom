package graph

import "time"

// Location is a composite key that uniquely identifies a single checkpoint
// within a thread. All three fields are required for point-in-time lookups;
// when CheckpointID is empty, the most recent checkpoint in the thread is used.
type Location struct {
	// ThreadID is the ID of the thread that this checkpoint belongs to.
	ThreadID string `json:"thread_id"`

	// CheckpointNS is the namespace of the checkpoint that this snapshot belongs to.
	CheckpointNS string `json:"checkpoint_ns"`

	// CheckpointID is the ID of the checkpoint that this snapshot belongs to.
	CheckpointID string `json:"checkpoint_id"`
}

// Snapshot captures the full execution state of a graph at a single point in
// time. It is the value type exchanged between [Graph.Execute], the
// [Checkpointer], and callers resuming a paused graph.
type Snapshot[S State[S]] struct {
	State      S         `json:"state"`
	Next       []string  `json:"next"`
	Location   Location  `json:"location"`
	Parent     *Location `json:"parent,omitempty"`
	CreateTime time.Time `json:"create_time"`
}

func (s Snapshot[State]) Copy() Snapshot[State] {
	cp := s
	cp.State = s.State.Copy()

	if len(s.Next) > 0 {
		cp.Next = make([]string, len(s.Next))
		copy(cp.Next, s.Next)
	}

	if s.Parent != nil {
		parent := *s.Parent
		cp.Parent = &parent
	}

	return cp
}

// IsDone reports whether the graph has finished executing.
// The graph is done when the Next slice is empty, or when every entry in Next
// equals [END].
func (s Snapshot[State]) IsDone() bool {
	if len(s.Next) == 0 {
		return true
	}

	for _, next := range s.Next {
		if next != END {
			return false
		}
	}

	return true
}
