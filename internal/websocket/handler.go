// Package websocket provides the WebSocket-to-PTY bridge for browser terminals.
//
// Architecture:
//
//   Browser (xterm.js)
//       ↕ WebSocket connection (binary frames)
//   This handler
//       ↕ Read/Write to PTY master fd
//   Container process (bash)
//
// Two goroutines per connection:
//   - Read pump:  WebSocket → PTY (user keystrokes reach the shell)
//   - Write pump: PTY → WebSocket (shell output reaches the browser)
//
// The write pump is handled by the manager's readLoop, which calls our
// registered writer function. This handler only needs to handle the read pump
// and register/deregister the writer.
//
// Message protocol:
//   - Binary messages: raw terminal I/O (keystrokes and output)
//   - Text messages:   JSON control commands (e.g., terminal resize)
//
// Example resize message: {"type":"resize","rows":24,"cols":80}
package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"Container2Go/internal/auth"
	"Container2Go/internal/manager"
)

// upgrader configures the WebSocket upgrade parameters.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Allow connections from any origin (for development).
	// In production, restrict this to your domain.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// resizeMsg is the JSON format for terminal resize commands.
type resizeMsg struct {
	Type string `json:"type"`
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// Handler holds dependencies for the WebSocket endpoint.
type Handler struct {
	Auth    *auth.Service
	Manager *manager.Manager
}

// ServeWS handles WebSocket connections for terminal sessions.
//
// URL format: /ws/{containerID}
//
// Flow:
//   1. Authenticate via session cookie
//   2. Look up the container session
//   3. Upgrade HTTP to WebSocket
//   4. Register writer with the container session (for output streaming)
//   5. Read user input from WebSocket and write to PTY
//   6. On disconnect, deregister writer (container stays alive)
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Authenticate
	username, err := h.Auth.GetUsernameFromRequest(r)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Extract container ID from URL path: /ws/{id}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		http.Error(w, "Missing container ID", http.StatusBadRequest)
		return
	}
	containerID := parts[1]

	// Look up the session
	session, ok := h.Manager.GetSession(containerID)
	if !ok {
		http.Error(w, "Container not found", http.StatusNotFound)
		return
	}

	// Verify the user owns this container
	if session.Container.UserID != username {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("WebSocket connected: user=%s container=%s", username, containerID)

	// Register this WebSocket as the output writer.
	// The manager's readLoop will call this function whenever
	// the container produces output.
	session.AttachWriter(func(data []byte) error {
		return conn.WriteMessage(websocket.BinaryMessage, data)
	})

	// Deregister on disconnect
	defer func() {
		session.AttachWriter(nil)
		log.Printf("WebSocket disconnected: user=%s container=%s", username, containerID)
	}()

	// Read pump: WebSocket → PTY
	// This loop reads user input (keystrokes) from the WebSocket
	// and writes them to the PTY master, which delivers them to
	// the container's bash process.
	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			return
		}

		switch msgType {
		case websocket.BinaryMessage:
			// Raw terminal input — write directly to PTY
			if err := session.WriteInput(msg); err != nil {
				log.Printf("PTY write error: %v", err)
				return
			}

		case websocket.TextMessage:
			// JSON control message (e.g., resize)
			var resize resizeMsg
			if err := json.Unmarshal(msg, &resize); err != nil {
				continue
			}
			if resize.Type == "resize" && resize.Rows > 0 && resize.Cols > 0 {
				if err := session.Resize(resize.Rows, resize.Cols); err != nil {
					log.Printf("PTY resize error: %v", err)
				}
			}
		}
	}
}
