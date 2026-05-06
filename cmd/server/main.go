// MiniDocker Server — Browser-based multi-user container platform.
//
// This is the main HTTP server that serves:
//   - Static frontend files (login page, terminal page)
//   - REST API for authentication and container management
//   - WebSocket endpoint for live terminal sessions
//
// It also handles the container re-exec pattern: when this binary is
// called with "child" as the first argument, it enters the container
// child process instead of starting the HTTP server.
//
// Usage:
//   sudo ./minidocker-server              # Start the HTTP server
//   # (internally re-execs as child)      # Container init process
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"Container2Go/internal/auth"
	"Container2Go/internal/manager"
	"Container2Go/internal/runtime"
	wsHandler "Container2Go/internal/websocket"
)

func main() {
	// --- Re-exec detection ---
	// When the server creates a container, it re-executes itself with
	// /proc/self/exe child <id> <rootfs> <cmd>
	// This check must happen before anything else.
	if len(os.Args) > 1 && os.Args[1] == "child" {
		runtime.RunChild(os.Args[2:])
		return
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting MiniDocker Server...")

	// Initialize services
	authService := auth.New()
	mgr := manager.New()
	wsH := &wsHandler.Handler{
		Auth:    authService,
		Manager: mgr,
	}

	// Determine frontend directory path
	execPath, _ := os.Executable()
	baseDir := filepath.Dir(execPath)
	frontendDir := filepath.Join(baseDir, "frontend")

	// Fallback: check if frontend is in current working directory
	if _, err := os.Stat(frontendDir); os.IsNotExist(err) {
		cwd, _ := os.Getwd()
		frontendDir = filepath.Join(cwd, "frontend")
	}

	log.Printf("Serving frontend from: %s", frontendDir)

	// --- Routes ---
	mux := http.NewServeMux()

	// Static frontend files
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			http.ServeFile(w, r, filepath.Join(frontendDir, "index.html"))
			return
		}
		if r.URL.Path == "/terminal" || r.URL.Path == "/terminal.html" {
			// Require auth for terminal page
			if _, err := authService.GetUsernameFromRequest(r); err != nil {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			http.ServeFile(w, r, filepath.Join(frontendDir, "terminal.html"))
			return
		}
		// Serve other static files (CSS, JS)
		http.ServeFile(w, r, filepath.Join(frontendDir, r.URL.Path))
	})

	// --- Auth API ---
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var creds struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			jsonError(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		token, err := authService.Login(creds.Username, creds.Password)
		if err != nil {
			jsonError(w, "Invalid credentials", http.StatusUnauthorized)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "session_token",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			MaxAge:   86400, // 24 hours
		})

		jsonResponse(w, map[string]string{
			"status":   "ok",
			"username": creds.Username,
		})
	})

	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_token")
		if err == nil {
			authService.Logout(cookie.Value)
		}
		http.SetCookie(w, &http.Cookie{
			Name:   "session_token",
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
		jsonResponse(w, map[string]string{"status": "ok"})
	})

	// --- Container API ---
	mux.HandleFunc("/api/containers", func(w http.ResponseWriter, r *http.Request) {
		username, err := authService.GetUsernameFromRequest(r)
		if err != nil {
			jsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		switch r.Method {
		case http.MethodGet:
			// List user's containers
			containers, _ := runtime.ListUserContainers(username)
			jsonResponse(w, containers)

		case http.MethodPost:
			// Create or get existing container
			session, err := mgr.GetOrCreateContainer(username)
			if err != nil {
				jsonError(w, fmt.Sprintf("Failed to create container: %v", err), http.StatusInternalServerError)
				return
			}
			jsonResponse(w, session.Container)

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/containers/", func(w http.ResponseWriter, r *http.Request) {
		username, err := authService.GetUsernameFromRequest(r)
		if err != nil {
			jsonError(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Parse: /api/containers/{id} or /api/containers/{id}/logs
		path := strings.TrimPrefix(r.URL.Path, "/api/containers/")
		parts := strings.Split(path, "/")
		containerID := parts[0]

		if len(parts) >= 2 && parts[1] == "logs" {
			// GET /api/containers/{id}/logs
			serveContainerLogs(w, containerID)
			return
		}

		if r.Method == http.MethodDelete {
			// DELETE /api/containers/{id}
			session, ok := mgr.GetSession(containerID)
			if !ok {
				jsonError(w, "Container not found", http.StatusNotFound)
				return
			}
			if session.Container.UserID != username {
				jsonError(w, "Forbidden", http.StatusForbidden)
				return
			}
			if err := mgr.StopContainer(containerID); err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			jsonResponse(w, map[string]string{"status": "stopped"})
			return
		}

		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	// --- WebSocket terminal ---
	mux.HandleFunc("/ws/", wsH.ServeWS)

	// --- Start server ---
	server := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down server...")
		server.Close()
	}()

	log.Printf("Server listening on http://0.0.0.0:8080")
	log.Printf("Demo users: alice/password, bob/password, admin/admin")
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

// serveContainerLogs reads and returns a container's log file.
func serveContainerLogs(w http.ResponseWriter, containerID string) {
	logPath := filepath.Join(runtime.ContainerPath(containerID), "logs.txt")
	data, err := os.ReadFile(logPath)
	if err != nil {
		jsonError(w, "Logs not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write(data)
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
