package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/masterkeysrd/loom/graph"
	"github.com/masterkeysrd/loom/message"
	"github.com/masterkeysrd/loom/stream"
)

// GraphOptions contains registration options for a Graph.
type GraphOptions struct {
	DisplayName string
	Commands    []any // Struct instances that represent custom commands
}

type registryEntry struct {
	graph       any
	displayName string
	commands    []any
	inputSchema *jsonschema.Schema
	execute     func(ctx context.Context, cmdName string, payload []byte, loc *graph.Location) (any, error)
	load        func(ctx context.Context, loc graph.Location) (any, error)
	history     func(ctx context.Context, threadID string, ns string) ([]graph.Checkpoint, error)
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]registryEntry)
	workerID   = uuid.New().String()

	connMu       sync.Mutex
	cancelConn   context.CancelFunc
	outgoingChan chan Message
	outgoingMu   sync.Mutex
)

func publishMessage(msg Message) {
	outgoingMu.Lock()
	defer outgoingMu.Unlock()
	if outgoingChan != nil {
		select {
		case outgoingChan <- msg:
		default:
		}
	}
}

func findMessageListFields(t reflect.Type) map[string]bool {
	fields := make(map[string]bool)
	if t.Kind() != reflect.Struct {
		return fields
	}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		isMsgList := false
		if f.Type.Kind() == reflect.Slice {
			elem := f.Type.Elem()
			if (elem.Kind() == reflect.Interface && elem.Name() == "Message" && strings.HasSuffix(elem.PkgPath(), "/message")) ||
				(f.Type.Name() == "MessageList" && strings.HasSuffix(f.Type.PkgPath(), "/message")) {
				isMsgList = true
			}
		}

		if isMsgList {
			jsonName := f.Name
			if tag := f.Tag.Get("json"); tag != "" {
				parts := strings.Split(tag, ",")
				if parts[0] != "" && parts[0] != "-" {
					jsonName = parts[0]
				}
			}
			fields[jsonName] = true
		}
	}

	return fields
}

// RegisterGraph saves the graph instance and its options into the global registry.
func RegisterGraph[S graph.State[S]](g *graph.Graph[S], opts GraphOptions) {
	registryMu.Lock()
	defer registryMu.Unlock()

	// Wrap checkpointer to publish checkpoints to studio UI
	original := g.Checkpointer()
	if _, ok := original.(*studioCheckpointer); !ok {
		g.SetCheckpointer(&studioCheckpointer{original: original})
	}

	// Generate input schema for the state
	var inputSchema *jsonschema.Schema
	if schema, err := jsonschema.For[S](nil); err == nil {
		inputSchema = schema

		// Set semantic flags for message list properties
		msgListFields := findMessageListFields(reflect.TypeOf((*S)(nil)).Elem())
		for name, prop := range inputSchema.Properties {
			if msgListFields[name] {
				if prop.Extra == nil {
					prop.Extra = make(map[string]any)
				}
				prop.Extra["x-loom-content"] = "chat"
				prop.Extra["x-loom-type"] = "message_list"

				if prop.Items == nil {
					prop.Items = &jsonschema.Schema{}
				}
				prop.Items.Type = "object"
			}
		}

		InspectSchema(inputSchema)
	}

	executeFunc := func(ctx context.Context, cmdName string, payload []byte, loc *graph.Location) (any, error) {
		var cmd graph.Command[S]

		if cmdName == "" {
			var parsedState S
			if err := json.Unmarshal(payload, &parsedState); err != nil {
				return nil, fmt.Errorf("failed to unmarshal state: %w", err)
			}
			cmd = graph.Update[S](func(s S) S {
				return parsedState
			})
		} else {
			var foundCmd any
			for _, registeredCmd := range opts.Commands {
				t := reflect.TypeOf(registeredCmd)
				if t.Kind() == reflect.Ptr {
					t = t.Elem()
				}
				if t.Name() == cmdName {
					foundCmd = registeredCmd
					break
				}
			}

			if foundCmd == nil {
				return nil, fmt.Errorf("unknown command: %s", cmdName)
			}

			t := reflect.TypeOf(foundCmd)
			isPtr := t.Kind() == reflect.Ptr
			if isPtr {
				t = t.Elem()
			}

			val := reflect.New(t)
			if err := json.Unmarshal(payload, val.Interface()); err != nil {
				return nil, fmt.Errorf("failed to unmarshal command payload: %w", err)
			}

			var ok bool
			if isPtr {
				cmd, ok = val.Interface().(graph.Command[S])
			} else {
				cmd, ok = val.Elem().Interface().(graph.Command[S])
			}

			if !ok {
				return nil, fmt.Errorf("command %s does not implement graph.Command", cmdName)
			}
		}

		if loc == nil {
			loc = &graph.Location{
				ThreadID:     uuid.New().String(),
				CheckpointID: uuid.New().String(),
			}
		}
		events, err := g.Stream(ctx, cmd, loc)
		if err != nil {
			return nil, err
		}

		for ev, err := range events {
			if err != nil {
				continue
			}

			switch ev.Event {
			case "on_checkpoint_saved":
				if loc, ok := ev.Data.(graph.Location); ok {
					if snap, err := g.Load(ctx, loc); err == nil && snap != nil {
						decomposed, err := graph.DecomposeState(snap.State)
						if err != nil {
							continue
						}
						checkpoint := graph.Checkpoint{
							Location:  snap.Location,
							Parent:    snap.Parent,
							State:     decomposed,
							Next:      snap.Next,
							Timestamp: snap.CreateTime,
							Metadata:  snap.Metadata,
						}
						checkpointBytes, _ := json.Marshal(checkpoint)
						publishMessage(Message{
							Type: "on_checkpoint",
							Data: checkpointBytes,
						})
					}
				}
			case "on_llm_request":
				payload := map[string]any{
					"node":    ev.Node,
					"source":  ev.Source,
					"request": ev.Data,
				}
				payloadBytes, _ := json.Marshal(payload)
				publishMessage(Message{
					Type: "on_llm_request",
					Data: payloadBytes,
				})
			case "on_llm_chunk":
				if chunk, ok := ev.Data.(message.AssistantChunk); ok {
					payload := map[string]any{
						"node":   ev.Node,
						"source": ev.Source,
						"chunk":  chunk,
					}
					payloadBytes, _ := json.Marshal(payload)
					publishMessage(Message{
						Type: "on_llm_chunk",
						Data: payloadBytes,
					})
				}
			case "on_tool_chunk":
				if chunk, ok := ev.Data.(message.ToolChunk); ok {
					payload := map[string]any{
						"node":   ev.Node,
						"source": ev.Source,
						"chunk":  chunk,
					}
					payloadBytes, _ := json.Marshal(payload)
					publishMessage(Message{
						Type: "on_tool_chunk",
						Data: payloadBytes,
					})
				}
			}
		}

		return nil, nil
	}

	registry[g.Name()] = registryEntry{
		graph:       g,
		displayName: opts.DisplayName,
		commands:    opts.Commands,
		inputSchema: inputSchema,
		execute:     executeFunc,
		load: func(ctx context.Context, loc graph.Location) (any, error) {
			snap, err := g.Load(ctx, loc)
			if err != nil {
				return nil, err
			}
			return snap.State, nil
		},
		history: func(ctx context.Context, threadID string, ns string) ([]graph.Checkpoint, error) {
			cp := g.Checkpointer()
			if cp == nil {
				return nil, fmt.Errorf("no checkpointer configured")
			}

			// We need to unwrap studioCheckpointer if present
			type unwrapper interface {
				Unwrap() graph.Checkpointer
			}
			actualCP := cp
			for {
				if uw, ok := actualCP.(unwrapper); ok {
					actualCP = uw.Unwrap()
				} else if sc, ok := actualCP.(*studioCheckpointer); ok {
					actualCP = sc.original
				} else {
					break
				}
			}

			if actualCP == nil {
				return nil, fmt.Errorf("no checkpointer configured")
			}

			var list []graph.Checkpoint
			// Start with the latest checkpoint (empty CheckpointID)
			loc := graph.Location{
				ThreadID:     threadID,
				CheckpointNS: ns,
			}

			for {
				checkpoint, err := actualCP.Load(ctx, loc)
				if err != nil {
					return nil, err
				}
				if checkpoint == nil {
					break
				}
				list = append(list, *checkpoint)
				if checkpoint.Parent == nil || checkpoint.Parent.CheckpointID == "" {
					break
				}
				loc = *checkpoint.Parent
			}

			// Reverse the list to make it chronological
			for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
				list[i], list[j] = list[j], list[i]
			}

			return list, nil
		},
	}
}

// Connect dials the Studio control WebSocket and handles heartbeats to keep the connection alive.
func Connect(ctx context.Context, wsURL string) error {
	connMu.Lock()
	defer connMu.Unlock()

	stream.SetGlobalWriter(&studioGlobalStreamWriter{})

	if cancelConn != nil {
		cancelConn()
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	cancelConn = cancel

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(sessionCtx, wsURL, nil)
	if err != nil {
		go reconnectLoop(sessionCtx, wsURL)
		return err
	}

	manifest, err := buildManifest(workerID)
	if err != nil {
		conn.Close()
		go reconnectLoop(sessionCtx, wsURL)
		return fmt.Errorf("failed to build manifest: %w", err)
	}

	if err := conn.WriteJSON(manifest); err != nil {
		conn.Close()
		go reconnectLoop(sessionCtx, wsURL)
		return fmt.Errorf("failed to send manifest: %w", err)
	}

	go runSession(sessionCtx, conn, wsURL)
	return nil
}

func reconnectLoop(ctx context.Context, wsURL string) {
	backoff := 1 * time.Second
	maxBackoff := 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			dialer := websocket.DefaultDialer
			conn, _, err := dialer.DialContext(ctx, wsURL, nil)
			if err != nil {
				backoff = backoff * 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}

			manifest, err := buildManifest(workerID)
			if err != nil {
				conn.Close()
				continue
			}

			if err := conn.WriteJSON(manifest); err != nil {
				conn.Close()
				continue
			}

			runSession(ctx, conn, wsURL)
			return
		}
	}
}

func runSession(ctx context.Context, conn *websocket.Conn, wsURL string) {
	defer conn.Close()

	send := make(chan Message, 256)

	outgoingMu.Lock()
	outgoingChan = send
	outgoingMu.Unlock()

	defer func() {
		outgoingMu.Lock()
		if outgoingChan == send {
			outgoingChan = nil
		}
		outgoingMu.Unlock()
	}()

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case msg, ok := <-send:
				if !ok {
					conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					return
				}
				if err := conn.WriteJSON(msg); err != nil {
					cancel()
					return
				}
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					cancel()
					return
				}
			case <-sessionCtx.Done():
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
		}
	}()

	conn.SetPongHandler(func(string) error {
		return nil
	})

	for {
		var msg Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			break
		}

		handleIncomingMessage(ctx, msg, send)
	}

	cancel()
	conn.Close()

	select {
	case <-ctx.Done():
		return
	default:
		go reconnectLoop(ctx, wsURL)
	}
}

type ExecutePayload struct {
	WorkerID     string          `json:"worker_id"`
	GraphID      string          `json:"graph_id"`
	CommandName  string          `json:"command_name"`
	Payload      json.RawMessage `json:"payload"`
	ThreadID     string          `json:"thread_id,omitempty"`
	CheckpointID string          `json:"checkpoint_id,omitempty"`
	CheckpointNS string          `json:"checkpoint_ns,omitempty"`
}

func handleIncomingMessage(ctx context.Context, msg Message, send chan<- Message) {
	if msg.Type == "execute" {
		var execPayload ExecutePayload
		if err := json.Unmarshal(msg.Data, &execPayload); err != nil {
			return
		}

		registryMu.RLock()
		entry, ok := registry[execPayload.GraphID]
		registryMu.RUnlock()
		if !ok {
			return
		}

		go func() {
			var loc *graph.Location
			if execPayload.ThreadID != "" {
				loc = &graph.Location{
					ThreadID:     execPayload.ThreadID,
					CheckpointID: execPayload.CheckpointID,
					CheckpointNS: execPayload.CheckpointNS,
				}
			}
			_, _ = entry.execute(ctx, execPayload.CommandName, execPayload.Payload, loc)
		}()
	} else if msg.Type == "load_checkpoint" {
		var payload struct {
			GraphID      string `json:"graph_id"`
			ThreadID     string `json:"thread_id"`
			CheckpointID string `json:"checkpoint_id"`
			CheckpointNS string `json:"checkpoint_ns"`
		}
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			return
		}

		registryMu.RLock()
		entry, ok := registry[payload.GraphID]
		registryMu.RUnlock()
		if !ok {
			return
		}

		go func() {
			loc := graph.Location{
				ThreadID:     payload.ThreadID,
				CheckpointID: payload.CheckpointID,
				CheckpointNS: payload.CheckpointNS,
			}
			state, err := entry.load(ctx, loc)
			if err != nil {
				errPayload := map[string]string{"error": err.Error()}
				errBytes, _ := json.Marshal(errPayload)
				send <- Message{
					Type:          "on_checkpoint_loaded",
					CorrelationID: msg.CorrelationID,
					Data:          errBytes,
				}
				return
			}

			stateBytes, _ := json.Marshal(state)
			send <- Message{
				Type:          "on_checkpoint_loaded",
				CorrelationID: msg.CorrelationID,
				Data:          stateBytes,
			}
		}()
	} else if msg.Type == "load_checkpoint_history" {
		var payload struct {
			GraphID      string `json:"graph_id"`
			ThreadID     string `json:"thread_id"`
			CheckpointNS string `json:"checkpoint_ns"`
		}
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			return
		}

		registryMu.RLock()
		entry, ok := registry[payload.GraphID]
		registryMu.RUnlock()
		if !ok {
			return
		}

		go func() {
			historyList, err := entry.history(ctx, payload.ThreadID, payload.CheckpointNS)
			if err != nil {
				errPayload := map[string]string{"error": err.Error()}
				errBytes, _ := json.Marshal(errPayload)
				send <- Message{
					Type:          "on_checkpoint_history_loaded",
					CorrelationID: msg.CorrelationID,
					Data:          errBytes,
				}
				return
			}

			historyBytes, _ := json.Marshal(historyList)
			send <- Message{
				Type:          "on_checkpoint_history_loaded",
				CorrelationID: msg.CorrelationID,
				Data:          historyBytes,
			}
		}()
	}
}

func buildManifest(wID string) (*Manifest, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	m := &Manifest{
		Type:     "manifest",
		WorkerID: wID,
		Graphs:   make([]GraphManifest, 0, len(registry)),
	}

	for name, entry := range registry {
		gm, err := buildGraphManifestFromEntry(name, entry)
		if err != nil {
			return nil, err
		}
		m.Graphs = append(m.Graphs, gm)
	}

	return m, nil
}

func buildGraphManifestFromEntry(name string, entry registryEntry) (GraphManifest, error) {
	v := reflect.ValueOf(entry.graph)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return GraphManifest{}, fmt.Errorf("graph must be a non-nil pointer")
	}

	mermaidMethod := v.MethodByName("ToMermaid")
	if !mermaidMethod.IsValid() {
		return GraphManifest{}, fmt.Errorf("graph does not have a ToMermaid() method")
	}
	mermaidVal := mermaidMethod.Call(nil)

	commands := make([]CommandDefinition, 0, len(entry.commands))
	for _, cmd := range entry.commands {
		t := reflect.TypeOf(cmd)
		if t.Kind() == reflect.Ptr {
			t = t.Elem()
		}
		cmdName := t.Name()
		schema, err := jsonschema.ForType(t, nil)
		if err != nil {
			return GraphManifest{}, fmt.Errorf("failed to generate schema for command %s: %w", cmdName, err)
		}
		InspectSchema(schema)
		commands = append(commands, CommandDefinition{
			Name:   cmdName,
			Schema: schema,
		})
	}

	gm := GraphManifest{
		ID:             name,
		Name:           entry.displayName,
		MermaidDiagram: mermaidVal[0].String(),
		InputSchema:    entry.inputSchema,
		Commands:       commands,
	}
	if gm.Name == "" {
		gm.Name = name
	}

	return gm, nil
}

type studioCheckpointer struct {
	original graph.Checkpointer
}

func (sc *studioCheckpointer) Unwrap() graph.Checkpointer {
	return sc.original
}

func (sc *studioCheckpointer) Load(ctx context.Context, loc graph.Location) (*graph.Checkpoint, error) {
	if sc.original == nil {
		return nil, graph.ErrCheckpointNotFound
	}
	return sc.original.Load(ctx, loc)
}

func (sc *studioCheckpointer) Record(ctx context.Context, cp graph.Checkpoint) error {
	var err error
	if sc.original != nil {
		err = sc.original.Record(ctx, cp)
	}

	cpBytes, _ := json.Marshal(cp)
	publishMessage(Message{
		Type: "on_checkpoint",
		Data: cpBytes,
	})

	return err
}

type studioGlobalStreamWriter struct{}

func (sw *studioGlobalStreamWriter) Write(ctx context.Context, data any) error {
	execCtx, ok := graph.ExecutionCtxFromContext(ctx)
	if !ok {
		return nil
	}
	metadata, _ := stream.MetadataFromContext(ctx)

	switch v := data.(type) {
	case message.AssistantChunk:
		payload := map[string]any{
			"node":   execCtx.NodeName,
			"source": metadata.Source,
			"chunk":  v,
		}
		payloadBytes, _ := json.Marshal(payload)
		publishMessage(Message{
			Type: "on_llm_chunk",
			Data: payloadBytes,
		})
	case message.ToolChunk:
		payload := map[string]any{
			"node":   execCtx.NodeName,
			"source": metadata.Source,
			"chunk":  v,
		}
		payloadBytes, _ := json.Marshal(payload)
		publishMessage(Message{
			Type: "on_tool_chunk",
			Data: payloadBytes,
		})
	case stream.Event:
		if v.Name == "on_llm_request" {
			payload := map[string]any{
				"node":    execCtx.NodeName,
				"source":  metadata.Source,
				"request": v.Data,
			}
			payloadBytes, _ := json.Marshal(payload)
			publishMessage(Message{
				Type: "on_llm_request",
				Data: payloadBytes,
			})
		}
	}

	return nil
}
