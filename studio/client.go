package studio

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// Connect dials the Studio control WebSocket and handles heartbeats to keep the connection alive.
// This is used by client applications to establish a bidirectional channel.
func Connect(ctx context.Context, wsURL string) (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to studio: %w", err)
	}

	// Start heartbeat goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-ctx.Done():
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				conn.Close()
				return
			}
		}
	}()

	return conn, nil
}
