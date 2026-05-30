package trace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const logPath = "logs/stream_chain.jsonl"

type contextKey struct{}

type Metadata struct {
	SessionID string
}

type Entry struct {
	Timestamp string `json:"timestamp"`
	SessionID string `json:"session_id,omitempty"`
	Component string `json:"component"`
	Stage     string `json:"stage"`
	Data      any    `json:"data,omitempty"`
}

var mu sync.Mutex

func WithSession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, Metadata{SessionID: sessionID})
}

func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	metadata, ok := ctx.Value(contextKey{}).(Metadata)
	if !ok {
		return ""
	}
	return metadata.SessionID
}

func Append(ctx context.Context, component, stage string, data any) {
	AppendSession(SessionIDFromContext(ctx), component, stage, data)
}

func AppendSession(sessionID, component, stage string, data any) {
	entry := Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		Component: component,
		Stage:     stage,
		Data:      data,
	}

	payload, err := json.Marshal(entry)
	if err != nil {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return
	}

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()

	_, _ = file.Write(append(payload, '\n'))
}
