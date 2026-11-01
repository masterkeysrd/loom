package pg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/masterkeysrd/loom/graph"
)

const (
	saveCheckpointQuery = `
INSERT INTO checkpoints (thread_id, checkpoint_ns, checkpoint_id, parent_checkpoint_id, checkpoint, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (thread_id, checkpoint_ns, checkpoint_id) DO UPDATE
SET parent_checkpoint_id = EXCLUDED.parent_checkpoint_id,
	checkpoint = EXCLUDED.checkpoint,
	metadata = EXCLUDED.metadata
`

	saveBlobQuery = `
INSERT INTO checkpoint_blobs (thread_id, checkpoint_ns, blob_id, value)
VALUES ($1, $2, $3, $4)
ON CONFLICT (thread_id, checkpoint_ns, blob_id) DO NOTHING
`

	saveWriteQuery = `
INSERT INTO checkpoint_writes (thread_id, checkpoint_ns, checkpoint_id, channel, blob_id, op)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (thread_id, checkpoint_ns, checkpoint_id, channel) DO UPDATE
SET blob_id = EXCLUDED.blob_id,
	op = EXCLUDED.op
`

	loadCheckpointQuery = `
SELECT thread_id, checkpoint_ns, checkpoint_id, parent_checkpoint_id, checkpoint, metadata
FROM checkpoints
WHERE thread_id = $1 AND checkpoint_ns = $2
%s
ORDER BY checkpoint_id DESC
LIMIT 1
`

	loadWritesQuery = `
WITH RECURSIVE path(id, parent, depth) AS (
    SELECT checkpoint_id, parent_checkpoint_id, 0
    FROM checkpoints 
    WHERE thread_id = $1 AND checkpoint_ns = $2 AND checkpoint_id = $3
    UNION ALL
    SELECT c.checkpoint_id, c.parent_checkpoint_id, p.depth + 1
    FROM path p
    JOIN checkpoints c ON c.thread_id = $4 AND c.checkpoint_ns = $5 AND c.checkpoint_id = p.parent
),
writes_with_seed AS (
    SELECT w.channel, coalesce(w.op, 'set') AS op, w.blob_id, p.depth,
           MIN(CASE WHEN coalesce(w.op, 'set') = 'set' THEN p.depth END) OVER (PARTITION BY w.channel) AS seed_depth
    FROM path p
    JOIN checkpoint_writes w
      ON w.thread_id = $6 AND w.checkpoint_ns = $7 AND w.checkpoint_id = p.id
)
SELECT ws.channel, ws.op, b.value
FROM writes_with_seed ws
JOIN checkpoint_blobs b
  ON b.thread_id = $8 AND b.checkpoint_ns = $9 AND b.blob_id = ws.blob_id
WHERE ws.seed_depth IS NOT NULL AND ws.depth <= ws.seed_depth
ORDER BY ws.channel ASC, ws.depth DESC
`
)

// Checkpointer persists graph checkpoints in a PostgreSQL database using a
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

// Record records checkpoint writes into PostgreSQL. When checkpoint.ChannelWrites
// is provided, only modified channels are recorded, applying WriteOpAppend for deltas
// and WriteOpSet for seeds.
func (c *Checkpointer) Record(ctx context.Context, checkpoint graph.Checkpoint) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Determine channel writes
	writes := checkpoint.ChannelWrites
	if writes == nil {
		if checkpoint.Parent != nil && checkpoint.Parent.CheckpointID != "" {
			parentCP, err := c.Load(ctx, *checkpoint.Parent)
			if err == nil && parentCP != nil {
				writes = graph.DiffState(parentCP.State, checkpoint.State)
			}
		}
		if writes == nil {
			writes = graph.DiffState(nil, checkpoint.State)
		}
	}

	// 2. Save blobs and writes
	for channel, cw := range writes {
		blobID := hashValue(cw.Value)

		if _, err := tx.ExecContext(ctx, saveBlobQuery,
			checkpoint.Location.ThreadID,
			checkpoint.Location.CheckpointNS,
			blobID,
			cw.Value,
		); err != nil {
			return fmt.Errorf("save blob for channel %q: %w", channel, err)
		}

		if _, err := tx.ExecContext(ctx, saveWriteQuery,
			checkpoint.Location.ThreadID,
			checkpoint.Location.CheckpointNS,
			checkpoint.Location.CheckpointID,
			channel,
			blobID,
			string(cw.Op),
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
		cond = "AND checkpoint_id = $3"
		args = append(args, location.CheckpointID)
	}

	query := fmt.Sprintf(loadCheckpointQuery, cond)

	var threadID, checkpointNS, checkpointID string
	var parentCheckpointID sql.NullString
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
	rows, err := c.db.QueryContext(ctx, loadWritesQuery,
		threadID, checkpointNS, checkpointID,
		threadID, checkpointNS,
		threadID, checkpointNS,
		threadID, checkpointNS,
	)
	if err != nil {
		return nil, fmt.Errorf("load writes: %w", err)
	}
	defer rows.Close()

	stateMap := make(map[string][]byte)
	for rows.Next() {
		var channel, op string
		var value []byte
		if err := rows.Scan(&channel, &op, &value); err != nil {
			return nil, err
		}
		newVal, err := graph.ApplyChannelWrite(stateMap[channel], graph.ChannelWrite{
			Op:    graph.WriteOp(op),
			Value: value,
		})
		if err != nil {
			return nil, fmt.Errorf("apply channel write for %q: %w", channel, err)
		}
		stateMap[channel] = newVal
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load writes rows: %w", err)
	}

	checkpoint := &graph.Checkpoint{
		Location: graph.Location{
			ThreadID:     threadID,
			CheckpointNS: checkpointNS,
			CheckpointID: checkpointID,
		},
		State:     stateMap,
		Next:      meta.Next,
		Timestamp: meta.Timestamp,
		Metadata:  metadataBytes,
	}

	if parentCheckpointID.Valid {
		checkpoint.Parent = &graph.Location{
			ThreadID:     threadID,
			CheckpointNS: checkpointNS,
			CheckpointID: parentCheckpointID.String,
		}
	}

	return checkpoint, nil
}

func hashValue(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type checkpointMetadata struct {
	Next      []string  `json:"next"`
	Timestamp time.Time `json:"timestamp"`
}
