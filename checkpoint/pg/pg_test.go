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
	"github.com/masterkeysrd/loom/message"
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

type SampleToolResult struct {
	Zebra string `json:"zebra"`
	Apple string `json:"apple"`
}

type AgentStateWithTool struct {
	Messages message.MessageList `json:"messages"`
}

func TestStructuredContent_PGCheckpointerDeterminism(t *testing.T) {
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
	loc := graph.Location{
		ThreadID:     "thread-tool-test-" + uuid.New().String(),
		CheckpointNS: "",
		CheckpointID: uuid.Must(uuid.NewV7()).String(),
	}

	toolMsg := &message.Tool{
		ToolCallID:        "call_123",
		Name:              "get_sample",
		StructuredContent: SampleToolResult{Zebra: "stripes", Apple: "red"},
	}

	state := AgentStateWithTool{
		Messages: message.MessageList{toolMsg},
	}

	// 1. Serialization BEFORE checkpoint:
	beforeToolBytes, err := json.Marshal(toolMsg)
	if err != nil {
		t.Fatalf("failed to marshal before checkpoint: %v", err)
	}

	beforeCanonBytes, err := toolMsg.CanonicalStructuredContent()
	if err != nil {
		t.Fatalf("failed to get canonical bytes before checkpoint: %v", err)
	}

	// 2. Record checkpoint into PostgreSQL
	channels, err := graph.DecomposeState(state)
	if err != nil {
		t.Fatalf("failed to decompose state: %v", err)
	}

	checkpoint := graph.Checkpoint{
		Location:  loc,
		State:     channels,
		Next:      []string{"next-node"},
		Timestamp: time.Now().UTC(),
	}

	if err := cp.Record(ctx, checkpoint); err != nil {
		t.Fatalf("failed to record checkpoint: %v", err)
	}

	// 3. Load checkpoint from PostgreSQL
	loadedCP, err := cp.Load(ctx, loc)
	if err != nil {
		t.Fatalf("failed to load checkpoint: %v", err)
	}
	if loadedCP == nil {
		t.Fatal("expected checkpoint to be found")
	}

	var loadedState AgentStateWithTool
	stateBytes, err := marshalStateMap(loadedCP.State)
	if err != nil {
		t.Fatalf("failed to marshal loaded state map: %v", err)
	}

	if err := json.Unmarshal(stateBytes, &loadedState); err != nil {
		t.Fatalf("failed to unmarshal loaded state: %v", err)
	}

	if len(loadedState.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loadedState.Messages))
	}

	loadedTool, ok := loadedState.Messages[0].(*message.Tool)
	if !ok {
		t.Fatalf("expected loaded message to be *message.Tool, got %T", loadedState.Messages[0])
	}

	// 4. Serialization AFTER checkpoint:
	afterToolBytes, err := json.Marshal(loadedTool)
	if err != nil {
		t.Fatalf("failed to marshal after checkpoint: %v", err)
	}

	afterCanonBytes, err := loadedTool.CanonicalStructuredContent()
	if err != nil {
		t.Fatalf("failed to get canonical bytes after checkpoint: %v", err)
	}

	// VERIFICATION: Serialization is 100% deterministic before and after PostgreSQL checkpoint!
	if string(beforeToolBytes) != string(afterToolBytes) {
		t.Fatalf("expected Tool JSON to be identical, but differed:\nBefore: %s\nAfter:  %s", string(beforeToolBytes), string(afterToolBytes))
	}

	if string(beforeCanonBytes) != string(afterCanonBytes) {
		t.Fatalf("expected CanonicalStructuredContent to be identical, but differed:\nBefore: %s\nAfter:  %s", string(beforeCanonBytes), string(afterCanonBytes))
	}
}

type MessagesState struct {
	Messages []string `json:"messages"`
}

func TestArrayAppendDeltaDeduplication(t *testing.T) {
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
	threadID := "thread-delta-" + uuid.New().String()

	var locs []graph.Location
	var prevChannels graph.StateMap
	var prevLoc *graph.Location

	var expectedHistory [][]string
	currentMessages := []string{}

	// Record 5 checkpoints appending 1 message per step
	for step := 1; step <= 5; step++ {
		msg := "message-" + string(rune('0'+step))
		currentMessages = append(currentMessages, msg)
		expectedHistory = append(expectedHistory, append([]string(nil), currentMessages...))

		st := MessagesState{Messages: currentMessages}
		channels, err := graph.DecomposeState(st)
		if err != nil {
			t.Fatal(err)
		}

		var writes map[string]graph.ChannelWrite
		if prevLoc == nil {
			writes = graph.DiffState(nil, channels)
		} else {
			writes = graph.DiffState(prevChannels, channels)
		}

		loc := graph.Location{
			ThreadID:     threadID,
			CheckpointNS: "",
			CheckpointID: uuid.Must(uuid.NewV7()).String(),
		}

		checkpoint := graph.Checkpoint{
			Location:      loc,
			Parent:        prevLoc,
			State:         channels,
			ChannelWrites: writes,
			Next:          []string{"next"},
			Timestamp:     time.Now().UTC(),
		}

		if err := cp.Record(ctx, checkpoint); err != nil {
			t.Fatalf("step %d: record failed: %v", step, err)
		}

		locs = append(locs, loc)
		prevChannels = channels
		prevLoc = &locs[len(locs)-1]
	}

	// Verify only 5 total blobs exist in CAS for this thread
	var blobCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM checkpoint_blobs WHERE thread_id = $1", threadID).Scan(&blobCount); err != nil {
		t.Fatal(err)
	}
	if blobCount != 5 {
		t.Fatalf("expected exactly 5 blobs (1 per appended message delta), got %d", blobCount)
	}

	// Verify time-travel and accurate state reconstruction for every checkpoint
	for i, loc := range locs {
		loaded, err := cp.Load(ctx, loc)
		if err != nil {
			t.Fatalf("failed to load checkpoint %d: %v", i+1, err)
		}
		if loaded == nil {
			t.Fatalf("checkpoint %d not found", i+1)
		}

		stateBytes, err := marshalStateMap(loaded.State)
		if err != nil {
			t.Fatal(err)
		}
		var loadedState MessagesState
		if err := json.Unmarshal(stateBytes, &loadedState); err != nil {
			t.Fatalf("unmarshal step %d state: %v", i+1, err)
		}

		if !reflect.DeepEqual(loadedState.Messages, expectedHistory[i]) {
			t.Fatalf("step %d reconstructed state mismatch:\nGot:  %v\nWant: %v", i+1, loadedState.Messages, expectedHistory[i])
		}
	}
}

func TestCompactionReplacementSeedReset(t *testing.T) {
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
	threadID := "thread-compaction-" + uuid.New().String()

	// Steps 1 to 3: append messages
	msgs := []string{"msg1"}
	s1 := MessagesState{Messages: msgs}
	ch1, _ := graph.DecomposeState(s1)
	loc1 := graph.Location{ThreadID: threadID, CheckpointID: uuid.Must(uuid.NewV7()).String()}
	if err := cp.Record(ctx, graph.Checkpoint{
		Location:      loc1,
		State:         ch1,
		ChannelWrites: graph.DiffState(nil, ch1),
		Next:          []string{"next"},
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	msgs = []string{"msg1", "msg2"}
	s2 := MessagesState{Messages: msgs}
	ch2, _ := graph.DecomposeState(s2)
	loc2 := graph.Location{ThreadID: threadID, CheckpointID: uuid.Must(uuid.NewV7()).String()}
	if err := cp.Record(ctx, graph.Checkpoint{
		Location:      loc2,
		Parent:        &loc1,
		State:         ch2,
		ChannelWrites: graph.DiffState(ch1, ch2),
		Next:          []string{"next"},
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	msgs = []string{"msg1", "msg2", "msg3"}
	s3 := MessagesState{Messages: msgs}
	ch3, _ := graph.DecomposeState(s3)
	loc3 := graph.Location{ThreadID: threadID, CheckpointID: uuid.Must(uuid.NewV7()).String()}
	if err := cp.Record(ctx, graph.Checkpoint{
		Location:      loc3,
		Parent:        &loc2,
		State:         ch3,
		ChannelWrites: graph.DiffState(ch2, ch3),
		Next:          []string{"next"},
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// Step 4: Compaction / replacement with summary
	msgs = []string{"summary_of_1_to_3"}
	s4 := MessagesState{Messages: msgs}
	ch4, _ := graph.DecomposeState(s4)
	diff4 := graph.DiffState(ch3, ch4)
	if diff4["messages"].Op != graph.WriteOpSet {
		t.Fatalf("expected step 4 to record WriteOpSet for compacted messages, got %s", diff4["messages"].Op)
	}

	loc4 := graph.Location{ThreadID: threadID, CheckpointID: uuid.Must(uuid.NewV7()).String()}
	if err := cp.Record(ctx, graph.Checkpoint{
		Location:      loc4,
		Parent:        &loc3,
		State:         ch4,
		ChannelWrites: diff4,
		Next:          []string{"next"},
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	var op4 string
	if err := db.QueryRow("SELECT op FROM checkpoint_writes WHERE thread_id = $1 AND checkpoint_id = $2", threadID, loc4.CheckpointID).Scan(&op4); err != nil {
		t.Fatal(err)
	}
	if op4 != "set" {
		t.Fatalf("expected step 4 op in database to be 'set', got %q", op4)
	}

	// Step 5: Append new message after compaction
	msgs = []string{"summary_of_1_to_3", "msg5"}
	s5 := MessagesState{Messages: msgs}
	ch5, _ := graph.DecomposeState(s5)
	diff5 := graph.DiffState(ch4, ch5)
	if diff5["messages"].Op != graph.WriteOpAppend {
		t.Fatalf("expected step 5 to record WriteOpAppend, got %s", diff5["messages"].Op)
	}

	loc5 := graph.Location{ThreadID: threadID, CheckpointID: uuid.Must(uuid.NewV7()).String()}
	if err := cp.Record(ctx, graph.Checkpoint{
		Location:      loc5,
		Parent:        &loc4,
		State:         ch5,
		ChannelWrites: diff5,
		Next:          []string{"next"},
		Timestamp:     time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	var op5 string
	if err := db.QueryRow("SELECT op FROM checkpoint_writes WHERE thread_id = $1 AND checkpoint_id = $2", threadID, loc5.CheckpointID).Scan(&op5); err != nil {
		t.Fatal(err)
	}
	if op5 != "append" {
		t.Fatalf("expected step 5 op in database to be 'append', got %q", op5)
	}

	// Load step 5: should reconstruct [summary_of_1_to_3, msg5] without reading steps 1..3
	loaded5, err := cp.Load(ctx, loc5)
	if err != nil {
		t.Fatalf("failed to load step 5: %v", err)
	}
	stateBytes5, err := marshalStateMap(loaded5.State)
	if err != nil {
		t.Fatal(err)
	}
	var loadedState5 MessagesState
	if err := json.Unmarshal(stateBytes5, &loadedState5); err != nil {
		t.Fatal(err)
	}

	expected5 := []string{"summary_of_1_to_3", "msg5"}
	if !reflect.DeepEqual(loadedState5.Messages, expected5) {
		t.Fatalf("step 5 mismatch: got %v, want %v", loadedState5.Messages, expected5)
	}

	// Time travel to step 2: should reconstruct [msg1, msg2]
	loaded2, err := cp.Load(ctx, loc2)
	if err != nil {
		t.Fatalf("failed to load step 2: %v", err)
	}
	stateBytes2, err := marshalStateMap(loaded2.State)
	if err != nil {
		t.Fatal(err)
	}
	var loadedState2 MessagesState
	if err := json.Unmarshal(stateBytes2, &loadedState2); err != nil {
		t.Fatal(err)
	}

	expected2 := []string{"msg1", "msg2"}
	if !reflect.DeepEqual(loadedState2.Messages, expected2) {
		t.Fatalf("step 2 mismatch: got %v, want %v", loadedState2.Messages, expected2)
	}
}
