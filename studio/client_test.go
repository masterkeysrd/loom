package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/masterkeysrd/loom/graph"
)

type TestState struct {
	Count    int      `json:"count"`
	History  []string `json:"history"`
	NextNode string   `json:"-"`
}

func (s TestState) Copy() TestState {
	hist := make([]string, len(s.History))
	copy(hist, s.History)
	return TestState{
		Count:    s.Count,
		History:  hist,
		NextNode: s.NextNode,
	}
}

type TestCommand struct {
	Increment int `json:"increment"`
}

func (c TestCommand) Apply(s TestState) TestState {
	s.Count += c.Increment
	return s
}

func TestRegisterAndConnect(t *testing.T) {
	// 1. Create a dummy graph
	builder := graph.New[TestState]().WithName("TestGraph")
	builder.AddNode("node1", graph.NodeFn[TestState](func(ctx context.Context, s TestState) (graph.Command[TestState], error) {
		return nil, nil
	}))
	builder.AddEdge(graph.START, "node1")
	builder.AddEdge("node1", graph.END)
	g, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}

	// 2. Register the graph
	RegisterGraph(g, GraphOptions{
		DisplayName: "My Awesome Test Graph",
		Commands:    []any{TestCommand{}},
	})

	// Verify the registry has the entry
	registryMu.RLock()
	entry, ok := registry["TestGraph"]
	registryMu.RUnlock()
	if !ok {
		t.Fatal("Expected TestGraph to be registered")
	}
	if entry.displayName != "My Awesome Test Graph" {
		t.Errorf("Expected displayName to be 'My Awesome Test Graph', got '%s'", entry.displayName)
	}

	// 3. Set up a test server to mock the Studio control WebSocket
	upgrader := websocket.Upgrader{}
	var manifestReceived *Manifest
	manifestChan := make(chan *Manifest, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade server connection: %v", err)
			return
		}
		defer conn.Close()

		// Read the manifest handshake
		var manifest Manifest
		if err := conn.ReadJSON(&manifest); err != nil {
			t.Errorf("Failed to read manifest: %v", err)
			return
		}
		manifestChan <- &manifest

		// Send execute command
		payload := []byte(`{"worker_id": "dummy", "graph_id": "TestGraph", "command_name": "TestCommand", "payload": {"increment": 5}}`)
		msg := Message{
			Type: "execute",
			Data: payload,
		}
		if err := conn.WriteJSON(msg); err != nil {
			t.Errorf("Failed to write execute command: %v", err)
			return
		}

		// Keep connection alive for a brief moment
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// 4. Connect to the test server
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = Connect(ctx, wsURL)
	if err != nil {
		t.Fatalf("Failed to connect to test server: %v", err)
	}

	// 5. Assert manifest was received and is correct
	select {
	case manifestReceived = <-manifestChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for manifest handshake")
	}

	if manifestReceived == nil {
		t.Fatal("Manifest was nil")
	}
	if len(manifestReceived.Graphs) != 1 {
		t.Fatalf("Expected 1 graph in manifest, got %d", len(manifestReceived.Graphs))
	}

	gm := manifestReceived.Graphs[0]
	if gm.ID != "TestGraph" || gm.Name != "My Awesome Test Graph" {
		t.Errorf("Incorrect manifest graph info: ID=%s, Name=%s", gm.ID, gm.Name)
	}

	if len(gm.Commands) != 1 {
		t.Fatalf("Expected 1 command in manifest, got %d", len(gm.Commands))
	}
	if gm.Commands[0].Name != "TestCommand" {
		t.Errorf("Expected command name to be TestCommand, got %s", gm.Commands[0].Name)
	}
}
