package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/masterkeysrd/loom/checkpoint/sqlite"
	"github.com/masterkeysrd/loom/graph"
	_ "modernc.org/sqlite"
)

type TestState struct {
	Count   int      `json:"count"`
	Message string   `json:"message"`
	Tags    []string `json:"tags"`
}

func TestCheckpointer(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cp, err := sqlite.NewCheckpointer(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	threadID := "thread-1"

	// 1. Record initial checkpoint
	state1 := TestState{Count: 1, Message: "hello", Tags: []string{"a"}}
	loc1 := graph.Location{
		ThreadID:     threadID,
		CheckpointNS: "",
		CheckpointID: uuid.Must(uuid.NewV7()).String(),
	}

	cp1 := graph.Checkpoint{
		Location:  loc1,
		State:     state1,
		Next:      []string{"node-2"},
		Timestamp: time.Now().UTC().Truncate(time.Second),
	}

	if err := cp.Record(ctx, cp1); err != nil {
		t.Fatalf("failed to record cp1: %v", err)
	}

	// 2. Load and verify
	loaded1, err := cp.Load(ctx, loc1)
	if err != nil {
		t.Fatalf("failed to load cp1: %v", err)
	}
	if loaded1 == nil {
		t.Fatal("expected cp1 to be found")
	}

	var loadedState1 TestState
	if err := json.Unmarshal(loaded1.State.(json.RawMessage), &loadedState1); err != nil {
		t.Fatalf("failed to unmarshal state1: %v", err)
	}

	if !reflect.DeepEqual(state1, loadedState1) {
		t.Errorf("expected state %+v, got %+v", state1, loadedState1)
	}

	// 3. Record second checkpoint (update one field)
	state2 := state1
	state2.Count = 2 // Only Count changes
	loc2 := graph.Location{
		ThreadID:     threadID,
		CheckpointNS: "",
		CheckpointID: uuid.Must(uuid.NewV7()).String(),
	}

	cp2 := graph.Checkpoint{
		Location:  loc2,
		Parent:    &loc1,
		State:     state2,
		Next:      []string{"node-3"},
		Timestamp: time.Now().UTC().Truncate(time.Second),
	}

	if err := cp.Record(ctx, cp2); err != nil {
		t.Fatalf("failed to record cp2: %v", err)
	}

	// 4. Verify CAS (Content-Addressable Storage)
	// Check how many unique blobs we have.
	// state1 has 3 fields. state2 has 1 field change.
	// Total should be: 3 (from state1) + 1 (new count from state2) = 4 blobs.
	var blobCount int
	err = db.QueryRow("SELECT COUNT(*) FROM checkpoint_blobs").Scan(&blobCount)
	if err != nil {
		t.Fatal(err)
	}
	if blobCount != 4 {
		t.Errorf("expected 4 blobs (Count1, Count2, Message, Tags), got %d", blobCount)
	}

	// 5. Verify state reconstruction
	loaded2, err := cp.Load(ctx, loc2)
	if err != nil {
		t.Fatalf("failed to load cp2: %v", err)
	}

	var loadedState2 TestState
	if err := json.Unmarshal(loaded2.State.(json.RawMessage), &loadedState2); err != nil {
		t.Fatalf("failed to unmarshal state2: %v", err)
	}

	if !reflect.DeepEqual(state2, loadedState2) {
		t.Errorf("expected state %+v, got %+v", state2, loadedState2)
	}

	// 6. Time travel (load loc1 again)
	loaded1Again, err := cp.Load(ctx, loc1)
	if err != nil {
		t.Fatal(err)
	}
	var loadedState1Again TestState
	json.Unmarshal(loaded1Again.State.(json.RawMessage), &loadedState1Again)
	if !reflect.DeepEqual(state1, loadedState1Again) {
		t.Errorf("time travel failed: expected %+v, got %+v", state1, loadedState1Again)
	}
}
