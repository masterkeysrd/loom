package pg_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/masterkeysrd/loom/checkpoint/pg"
	"github.com/masterkeysrd/loom/graph"
)

type TestState struct {
	Count   int      `json:"count"`
	Message string   `json:"message"`
	Tags    []string `json:"tags"`
}

func marshalStateMap(m map[string][]byte) ([]byte, error) {
	raw := make(map[string]json.RawMessage, len(m))
	for k, v := range m {
		raw[k] = json.RawMessage(v)
	}
	return json.Marshal(raw)
}

func TestCheckpointer(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cp, err := pg.NewCheckpointer(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	threadID := "thread-" + uuid.New().String()

	// 1. Record initial checkpoint
	state1 := TestState{Count: 1, Message: "hello", Tags: []string{"a"}}
	loc1 := graph.Location{
		ThreadID:     threadID,
		CheckpointNS: "",
		CheckpointID: uuid.Must(uuid.NewV7()).String(),
	}

	channels1, err := graph.DecomposeState(state1)
	if err != nil {
		t.Fatal(err)
	}

	cp1 := graph.Checkpoint{
		Location:  loc1,
		State:     channels1,
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
	stateBytes1, err := marshalStateMap(loaded1.State)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stateBytes1, &loadedState1); err != nil {
		t.Fatalf("failed to unmarshal state1: %v", err)
	}

	if !reflect.DeepEqual(state1, loadedState1) {
		t.Errorf("expected state %+v, got %+v", state1, loadedState1)
	}

	// 3. Record second checkpoint (update one field)
	state2 := state1
	state2.Count = 2
	loc2 := graph.Location{
		ThreadID:     threadID,
		CheckpointNS: "",
		CheckpointID: uuid.Must(uuid.NewV7()).String(),
	}

	channels2, err := graph.DecomposeState(state2)
	if err != nil {
		t.Fatal(err)
	}

	cp2 := graph.Checkpoint{
		Location:  loc2,
		Parent:    &loc1,
		State:     channels2,
		Next:      []string{"node-3"},
		Timestamp: time.Now().UTC().Truncate(time.Second),
	}

	if err := cp.Record(ctx, cp2); err != nil {
		t.Fatalf("failed to record cp2: %v", err)
	}

	// 4. Verify state reconstruction
	loaded2, err := cp.Load(ctx, loc2)
	if err != nil {
		t.Fatalf("failed to load cp2: %v", err)
	}

	var loadedState2 TestState
	stateBytes2, err := marshalStateMap(loaded2.State)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(stateBytes2, &loadedState2); err != nil {
		t.Fatalf("failed to unmarshal state2: %v", err)
	}

	if !reflect.DeepEqual(state2, loadedState2) {
		t.Errorf("expected state %+v, got %+v", state2, loadedState2)
	}
}
