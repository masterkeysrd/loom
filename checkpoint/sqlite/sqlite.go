package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/masterkeysrd/loom/graph"
)

const (
	saveCheckpointQuery = `
INSERT INTO checkpoints (thread_id, checkpoint_ns, checkpoint_id, parent_checkpoint_id, checkpoint, metadata)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (thread_id, checkpoint_ns, checkpoint_id) DO UPDATE
SET parent_checkpoint_id = EXCLUDED.parent_checkpoint_id,
	checkpoint = EXCLUDED.checkpoint,
	metadata = EXCLUDED.metadata
`

	saveBlobQuery = `
INSERT OR IGNORE INTO checkpoint_blobs (thread_id, checkpoint_ns, blob_id, value)
VALUES (?, ?, ?, ?)
`

	saveWriteQuery = `
INSERT INTO checkpoint_writes (thread_id, checkpoint_ns, checkpoint_id, channel, blob_id)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (thread_id, checkpoint_ns, checkpoint_id, channel) DO UPDATE
SET blob_id = EXCLUDED.blob_id
`

	loadCheckpointQuery = `
SELECT thread_id, checkpoint_ns, checkpoint_id, parent_checkpoint_id, checkpoint, metadata
FROM checkpoints
WHERE thread_id = ? AND checkpoint_ns = ?
%s
ORDER BY checkpoint_id DESC
LIMIT 1
`

	loadWritesQuery = `
WITH RECURSIVE path(id, parent) AS (
    SELECT checkpoint_id, parent_checkpoint_id FROM checkpoints 
    WHERE thread_id = ? AND checkpoint_ns = ? AND checkpoint_id = ?
    UNION ALL
    SELECT c.checkpoint_id, c.parent_checkpoint_id FROM checkpoints c
    JOIN path p ON c.checkpoint_id = p.parent
    WHERE c.thread_id = ? AND c.checkpoint_ns = ?
)
SELECT w.channel, w.blob_id, b.value
FROM checkpoint_writes w
JOIN checkpoint_blobs b ON w.thread_id = b.thread_id AND w.checkpoint_ns = b.checkpoint_ns AND w.blob_id = b.blob_id
WHERE w.thread_id = ? AND w.checkpoint_ns = ? AND w.checkpoint_id IN (SELECT id FROM path)
ORDER BY w.checkpoint_id ASC
`
)

// Checkpointer persists graph checkpoints in a SQLite database using a
// three-table schema (checkpoints, blobs, writes) for storage efficiency.
type Checkpointer struct {
	db *sql.DB
}

// NewCheckpointer opens a connection to db, runs any pending schema migrations,
// and returns a ready-to-use [Checkpointer].
func NewCheckpointer(db *sql.DB) (*Checkpointer, error) {
	if err := Migrate(db); err != nil {
		return nil, err
	}

	return &Checkpointer{db: db}, nil
}

// Record decomposes the checkpoint state into fields (channels) using reflection,
// deduplicates values via content-addressable storage (blobs), and records
// the pointers in the writes table.
func (c *Checkpointer) Record(ctx context.Context, checkpoint graph.Checkpoint) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Decompose state into channels
	channels, err := decomposeState(checkpoint.State)
	if err != nil {
		return fmt.Errorf("decompose state: %w", err)
	}

	// 2. Save blobs and writes
	for channel, value := range channels {
		blobID := hashValue(value)

		if _, err := tx.ExecContext(ctx, saveBlobQuery,
			checkpoint.Location.ThreadID,
			checkpoint.Location.CheckpointNS,
			blobID,
			value,
		); err != nil {
			return fmt.Errorf("save blob for channel %q: %w", channel, err)
		}

		if _, err := tx.ExecContext(ctx, saveWriteQuery,
			checkpoint.Location.ThreadID,
			checkpoint.Location.CheckpointNS,
			checkpoint.Location.CheckpointID,
			channel,
			blobID,
		); err != nil {
			return fmt.Errorf("save write for channel %q: %w", channel, err)
		}
	}

	// 3. Save checkpoint metadata
	meta := checkpointMetadata{
		Next:      checkpoint.Next,
		Timestamp: checkpoint.Timestamp,
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	parentID := sql.NullString{}
	if checkpoint.Parent != nil && checkpoint.Parent.CheckpointID != "" {
		parentID.String = checkpoint.Parent.CheckpointID
		parentID.Valid = true
	}

	if _, err := tx.ExecContext(ctx, saveCheckpointQuery,
		checkpoint.Location.ThreadID,
		checkpoint.Location.CheckpointNS,
		checkpoint.Location.CheckpointID,
		parentID,
		metaBytes,
		checkpoint.Metadata,
	); err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}

	return tx.Commit()
}

// Load reconstructs the state by collecting the latest version of each channel
// from the thread's history.
func (c *Checkpointer) Load(ctx context.Context, location graph.Location) (*graph.Checkpoint, error) {
	// 1. Find the target checkpoint
	args := []any{location.ThreadID, location.CheckpointNS}
	cond := ""
	if location.CheckpointID != "" {
		cond = "AND checkpoint_id = ?"
		args = append(args, location.CheckpointID)
	}

	query := fmt.Sprintf(loadCheckpointQuery, cond)

	var threadID, checkpointNS, checkpointID string
	var parentCheckpointID *string
	var metaBytes, metadataBytes []byte

	err := c.db.QueryRowContext(ctx, query, args...).Scan(
		&threadID,
		&checkpointNS,
		&checkpointID,
		&parentCheckpointID,
		&metaBytes,
		&metadataBytes,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	var meta checkpointMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, err
	}

	// 2. Reconstruct state from writes history
	// We use a recursive CTE to find all checkpoints in the ancestry and then
	// pick the latest value for each channel.
	rows, err := c.db.QueryContext(ctx, loadWritesQuery,
		threadID, checkpointNS, checkpointID,
		threadID, checkpointNS,
		threadID, checkpointNS,
	)
	if err != nil {
		return nil, fmt.Errorf("load writes: %w", err)
	}
	defer rows.Close()

	stateMap := make(map[string]json.RawMessage)
	for rows.Next() {
		var channel, blobID string
		var value []byte
		if err := rows.Scan(&channel, &blobID, &value); err != nil {
			return nil, err
		}
		stateMap[channel] = json.RawMessage(value)
	}

	// 3. Marshal the state map back to JSON so Graph.Load can unmarshal it into the struct.
	finalState, err := json.Marshal(stateMap)
	if err != nil {
		return nil, err
	}

	checkpoint := &graph.Checkpoint{
		Location: graph.Location{
			ThreadID:     threadID,
			CheckpointNS: checkpointNS,
			CheckpointID: checkpointID,
		},
		State:     json.RawMessage(finalState),
		Next:      meta.Next,
		Timestamp: meta.Timestamp,
		Metadata:  metadataBytes,
	}

	if parentCheckpointID != nil {
		checkpoint.Parent = &graph.Location{
			ThreadID:     threadID,
			CheckpointNS: checkpointNS,
			CheckpointID: *parentCheckpointID,
		}
	}

	return checkpoint, nil
}

func decomposeState(state any) (map[string][]byte, error) {
	if state == nil {
		return nil, nil
	}

	// If it's already json.RawMessage or []byte, we can't decompose it further without
	// knowing the original type. But since Graph passes the raw struct, we expect a struct.
	v := reflect.ValueOf(state)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		// Fallback to single "__root__" channel if not a struct
		data, err := json.Marshal(state)
		if err != nil {
			return nil, err
		}
		return map[string][]byte{"__root__": data}, nil
	}

	channels := make(map[string][]byte)
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

func hashValue(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type checkpointMetadata struct {
	Next      []string  `json:"next"`
	Timestamp time.Time `json:"timestamp"`
}
