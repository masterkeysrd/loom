package graph_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/masterkeysrd/loom/graph"
)

func TestDiffState_EmptyBefore(t *testing.T) {
	after := graph.StateMap{
		"count":    []byte("1"),
		"messages": []byte(`["hello"]`),
	}
	diff := graph.DiffState(nil, after)
	if len(diff) != 2 {
		t.Fatalf("expected 2 writes, got %d", len(diff))
	}
	if diff["count"].Op != graph.WriteOpSet || string(diff["count"].Value) != "1" {
		t.Errorf("unexpected count write: %+v", diff["count"])
	}
	if diff["messages"].Op != graph.WriteOpSet || string(diff["messages"].Value) != `["hello"]` {
		t.Errorf("unexpected messages write: %+v", diff["messages"])
	}
}

func TestDiffState_ArrayAppend(t *testing.T) {
	before := graph.StateMap{
		"count":    []byte("1"),
		"messages": []byte(`[{"role":"user","content":"hi"}]`),
	}
	after := graph.StateMap{
		"count":    []byte("1"),
		"messages": []byte(`[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]`),
	}

	diff := graph.DiffState(before, after)
	if len(diff) != 1 {
		t.Fatalf("expected 1 write (messages only), got %d", len(diff))
	}
	w, ok := diff["messages"]
	if !ok {
		t.Fatalf("expected messages in diff")
	}
	if w.Op != graph.WriteOpAppend {
		t.Fatalf("expected op append, got %s", w.Op)
	}
	expectedDelta := `[{"role":"assistant","content":"hello"}]`
	if string(w.Value) != expectedDelta {
		t.Fatalf("expected delta %s, got %s", expectedDelta, string(w.Value))
	}

	// ApplyChannelWrite verification
	stitched, err := graph.ApplyChannelWrite(before["messages"], w)
	if err != nil {
		t.Fatalf("failed to apply channel write: %v", err)
	}
	if !bytes.Equal(stitched, after["messages"]) {
		t.Fatalf("stitched mismatch:\nGot:  %s\nWant: %s", string(stitched), string(after["messages"]))
	}
}

func TestDiffState_ArrayCompactedOrReset(t *testing.T) {
	before := graph.StateMap{
		"messages": []byte(`[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]`),
	}
	after := graph.StateMap{
		"messages": []byte(`[{"role":"system","content":"summary"}]`),
	}

	diff := graph.DiffState(before, after)
	if len(diff) != 1 {
		t.Fatalf("expected 1 write, got %d", len(diff))
	}
	w := diff["messages"]
	if w.Op != graph.WriteOpSet {
		t.Fatalf("expected op set for compacted array, got %s", w.Op)
	}
	if !bytes.Equal(w.Value, after["messages"]) {
		t.Fatalf("expected value to be full after array")
	}

	stitched, err := graph.ApplyChannelWrite(before["messages"], w)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stitched, after["messages"]) {
		t.Fatalf("stitched mismatch")
	}
}

func TestApplyChannelWrite_ChainedAppends(t *testing.T) {
	step0 := []byte(`["msg0"]`)

	delta1 := graph.ChannelWrite{
		Op:    graph.WriteOpAppend,
		Value: []byte(`["msg1"]`),
	}
	step1, err := graph.ApplyChannelWrite(step0, delta1)
	if err != nil {
		t.Fatal(err)
	}
	if string(step1) != `["msg0","msg1"]` {
		t.Fatalf("unexpected step1: %s", string(step1))
	}

	delta2 := graph.ChannelWrite{
		Op:    graph.WriteOpAppend,
		Value: []byte(`["msg2","msg3"]`),
	}
	step2, err := graph.ApplyChannelWrite(step1, delta2)
	if err != nil {
		t.Fatal(err)
	}
	if string(step2) != `["msg0","msg1","msg2","msg3"]` {
		t.Fatalf("unexpected step2: %s", string(step2))
	}
}

func TestChannelWrite_JSONRoundTrip(t *testing.T) {
	cw := graph.ChannelWrite{
		Op:    graph.WriteOpAppend,
		Value: []byte(`[{"id":1}]`),
	}
	data, err := json.Marshal(cw)
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON := `{"op":"append","value":[{"id":1}]}`
	if string(data) != expectedJSON {
		t.Fatalf("expected %s, got %s", expectedJSON, string(data))
	}

	var restored graph.ChannelWrite
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Op != cw.Op || !bytes.Equal(restored.Value, cw.Value) {
		t.Fatalf("mismatch after unmarshal: %+v", restored)
	}
}
