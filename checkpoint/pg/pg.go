package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/masterkeysrd/loom/graph"
)

const (
	saveCheckpointQuery = `
INSERT INTO checkpoints (thread_id, checkpoint_ns, checkpoint_id, parent_checkpoint_id, state, next, timestamp)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (thread_id, checkpoint_ns, checkpoint_id) DO UPDATE
SET parent_checkpoint_id = EXCLUDED.parent_checkpoint_id,
	state = EXCLUDED.state,
	next = EXCLUDED.next,
	timestamp = EXCLUDED.timestamp
`

	loadCheckpointQuery = `
SELECT thread_id, checkpoint_ns, checkpoint_id, parent_checkpoint_id, state, next, timestamp
FROM checkpoints
WHERE thread_id = $1 AND checkpoint_ns = $2
%s
ORDER BY checkpoint_id DESC
LIMIT 1
`
)

// Checkpointer persists graph checkpoints in a PostgreSQL database.
// It implements [graph.Checkpointer] using a single checkpoints table that
// is created (if absent) by [NewCheckpointer] via the embedded migration runner.
type Checkpointer struct {
	db *sql.DB
}

// NewCheckpointer opens a connection to db, runs any pending schema migrations,
// and returns a ready-to-use [Checkpointer]. The caller retains ownership of db
// and is responsible for closing it.
func NewCheckpointer(db *sql.DB) (*Checkpointer, error) {
	if err := Migrate(db); err != nil {
		return nil, err
	}

	return &Checkpointer{db: db}, nil
}

// Record persists checkpoint to the checkpoints table. If a checkpoint with
// the same (thread_id, checkpoint_ns, checkpoint_id) already exists, its
// state, parent, next, and timestamp fields are overwritten.
func (c *Checkpointer) Record(ctx context.Context, checkpoint graph.Checkpoint) error {
	record := CheckpointRecord{
		ThreadID:     checkpoint.Location.ThreadID,
		CheckpointNS: checkpoint.Location.CheckpointNS,
		CheckpointID: checkpoint.Location.CheckpointID,
		State:        checkpoint.State,
		Timestamp:    checkpoint.Timestamp,
	}

	if checkpoint.Parent != nil && checkpoint.Parent.CheckpointID != "" {
		record.ParentCheckpointID = &checkpoint.Parent.CheckpointID
	}

	var err error
	record.Next, err = json.Marshal(checkpoint.Next)
	if err != nil {
		return err
	}

	if _, err := c.db.ExecContext(ctx,
		saveCheckpointQuery,
		record.ThreadID,
		record.CheckpointNS,
		record.CheckpointID,
		record.ParentCheckpointID,
		record.State,
		record.Next,
		record.Timestamp,
	); err != nil {
		return err
	}

	return nil
}

// Load retrieves the most-recent checkpoint for the given location.
// When location.CheckpointID is non-empty, only that exact checkpoint is
// returned; otherwise the latest checkpoint in the thread/namespace is used.
// Returns nil, nil when no matching row exists.
func (c *Checkpointer) Load(ctx context.Context, location graph.Location) (*graph.Checkpoint, error) {
	args := []any{location.ThreadID, location.CheckpointNS}
	cond := ""
	if location.CheckpointID != "" {
		cond = "AND checkpoint_id = $3"
		args = append(args, location.CheckpointID)
	}

	query := fmt.Sprintf(loadCheckpointQuery, cond)

	var record CheckpointRecord
	err := c.db.QueryRowContext(ctx,
		query,
		args...,
	).Scan(
		&record.ThreadID,
		&record.CheckpointNS,
		&record.CheckpointID,
		&record.ParentCheckpointID,
		&record.State,
		&record.Next,
		&record.Timestamp,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	var next []string
	if err := json.Unmarshal(record.Next, &next); err != nil {
		return nil, err
	}

	checkpoint := &graph.Checkpoint{
		Location: graph.Location{
			ThreadID:     record.ThreadID,
			CheckpointNS: record.CheckpointNS,
			CheckpointID: record.CheckpointID,
		},
		State:     record.State,
		Next:      next,
		Timestamp: record.Timestamp,
	}

	if record.ParentCheckpointID != nil {
		checkpoint.Parent = &graph.Location{
			ThreadID:     record.ThreadID,
			CheckpointNS: record.CheckpointNS,
			CheckpointID: *record.ParentCheckpointID,
		}
	}

	return checkpoint, nil
}

// CheckpointRecord is the database row representation used by [Checkpointer].
// State and Next are kept as raw JSON to avoid loading the full message/state
// type hierarchy inside the persistence layer.
type CheckpointRecord struct {
	ThreadID           string          `db:"thread_id"`
	CheckpointNS       string          `db:"checkpoint_ns"`
	CheckpointID       string          `db:"checkpoint_id"`
	ParentCheckpointID *string         `db:"parent_checkpoint_id"`
	State              json.RawMessage `db:"state"`
	Next               json.RawMessage `db:"next"`
	Timestamp          time.Time       `db:"timestamp"`
}
