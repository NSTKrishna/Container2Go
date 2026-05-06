// Package manager orchestrates container lifecycle, PTY sessions, and
// user-to-container mapping.
//
// It is the central coordination layer between:
//   - The HTTP/WebSocket handlers (user-facing)
//   - The container runtime (Linux primitives)
//   - PTY allocation (interactive terminal support)
//
// Key responsibilities:
//   - Map each user to exactly one persistent container
//   - Create containers on first login (provision rootfs, start process)
//   - Keep containers alive across WebSocket disconnects
//   - Bridge WebSocket connections to PTY sessions
//   - Clean up resources when containers stop
package manager

import (
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"Container2Go/internal/runtime"
)

// Manager tracks all active containers and their PTY sessions.
type Manager struct {
	sessions map[string]*ContainerSession // containerID → session
	userMap  map[string]string            // username → containerID
	mu       sync.RWMutex
}

// ContainerSession holds the live state of a running container.
// The PTY master file descriptor is owned by the Manager, not by
// individual WebSocket connections — this is what allows containers
// to survive WebSocket disconnects and reconnects.
type ContainerSession struct {
	Container *runtime.Container
	PTY       *os.File  // PTY master fd — reads get container output, writes send input
	Cmd       *exec.Cmd // The child process handle
	LogFile   *os.File  // Captures all container output for the logs command

	mu       sync.Mutex
	wsWriter func([]byte) error // Current WebSocket writer, nil if no client
	done     chan struct{}       // Closed when container process exits
}

// New creates a new container manager.
func New() *Manager {
	return &Manager{
		sessions: make(map[string]*ContainerSession),
		userMap:  make(map[string]string),
	}
}

// GetOrCreateContainer returns the user's existing container or creates a new one.
// Each user gets exactly one container that persists across sessions.
func (m *Manager) GetOrCreateContainer(username string) (*ContainerSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for existing container
	if containerID, ok := m.userMap[username]; ok {
		if session, ok := m.sessions[containerID]; ok {
			// Verify it's still alive
			select {
			case <-session.done:
				// Container exited — clean up and create a new one
				delete(m.sessions, containerID)
				delete(m.userMap, username)
			default:
				return session, nil
			}
		}
	}

	// Create new container
	return m.createContainerLocked(username)
}

// createContainerLocked creates a new container for a user. Caller must hold m.mu.
func (m *Manager) createContainerLocked(username string) (*ContainerSession, error) {
	containerID := generateID()
	rootfsPath := runtime.UserRootFSPath(username)

	// Provision the rootfs if it doesn't exist (copy from template)
	if err := provisionRootFS(username); err != nil {
		return nil, fmt.Errorf("provision rootfs: %w", err)
	}

	// Create metadata directory and log file
	containerDir := runtime.ContainerPath(containerID)
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		return nil, fmt.Errorf("create container dir: %w", err)
	}

	logPath := filepath.Join(containerDir, "logs.txt")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}

	// Build the child command.
	// /proc/self/exe re-executes this binary with "child" as the first arg.
	// The child process will run inside new Linux namespaces.
	childArgs := []string{"child", containerID, rootfsPath, "/bin/bash"}
	cmd := exec.Command("/proc/self/exe", childArgs...)

	// Linux namespace flags — these create isolation:
	//   CLONE_NEWUTS: new hostname namespace
	//   CLONE_NEWPID: new PID namespace (container sees PID 1)
	//   CLONE_NEWNS:  new mount namespace (mounts are isolated)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
		Unshareflags: syscall.CLONE_NEWNS,
	}

	// Start the child with a PTY (pseudo-terminal).
	//
	// Why PTY instead of pipes?
	// Pipes only carry raw bytes. A PTY provides:
	//   - Line discipline: Ctrl+C sends SIGINT, Ctrl+D sends EOF
	//   - Terminal size: programs like vim/less query rows/cols
	//   - Job control: foreground/background process groups
	//   - Raw mode: character-by-character input (for tab completion)
	//   - TTY detection: bash changes behavior if stdin is a TTY
	//
	// pty.StartWithAttrs merges our namespace flags with PTY requirements
	// (Setsid + Setctty) to make the child a session leader with a
	// controlling terminal.
	ptmx, err := pty.StartWithAttrs(cmd, nil, cmd.SysProcAttr)
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start container with PTY: %w", err)
	}

	// Save container metadata
	container := &runtime.Container{
		ID:        containerID,
		UserID:    username,
		PID:       cmd.Process.Pid,
		Command:   "/bin/bash",
		Status:    "running",
		RootFS:    rootfsPath,
		LogFile:   logPath,
		CreatedAt: time.Now(),
	}

	if err := runtime.SaveMetadata(container); err != nil {
		ptmx.Close()
		logFile.Close()
		return nil, fmt.Errorf("save metadata: %w", err)
	}

	session := &ContainerSession{
		Container: container,
		PTY:       ptmx,
		Cmd:       cmd,
		LogFile:   logFile,
		done:      make(chan struct{}),
	}

	// Start the output reader goroutine.
	// This continuously reads from the PTY master and:
	//   1. Writes to the log file (always)
	//   2. Sends to the WebSocket client (if connected)
	// Without this reader, the PTY buffer would fill up and block
	// the container process when no client is connected.
	go session.readLoop()

	// Wait for the container process to exit in the background.
	go func() {
		cmd.Wait()
		close(session.done)
		ptmx.Close()
		logFile.Close()
		container.Status = "exited"
		runtime.SaveMetadata(container)
		log.Printf("Container %s (user: %s) exited", containerID, username)
	}()

	m.sessions[containerID] = session
	m.userMap[username] = containerID

	log.Printf("Created container %s for user %s (PID: %d)", containerID, username, cmd.Process.Pid)
	return session, nil
}

// readLoop continuously reads PTY output and distributes it.
func (s *ContainerSession) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.PTY.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("PTY read error for %s: %v", s.Container.ID, err)
			}
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		// Always write to log file
		s.LogFile.Write(data)

		// Send to WebSocket if a client is connected
		s.mu.Lock()
		writer := s.wsWriter
		s.mu.Unlock()

		if writer != nil {
			if err := writer(data); err != nil {
				// WebSocket write failed — client probably disconnected
				s.mu.Lock()
				s.wsWriter = nil
				s.mu.Unlock()
			}
		}
	}
}

// AttachWriter sets the current WebSocket writer for live output streaming.
// Pass nil to detach.
func (s *ContainerSession) AttachWriter(writer func([]byte) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wsWriter = writer
}

// WriteInput sends data to the container's stdin (via PTY master).
// This is how user keystrokes reach the shell.
func (s *ContainerSession) WriteInput(data []byte) error {
	_, err := s.PTY.Write(data)
	return err
}

// Resize changes the PTY terminal size.
// Called when the user resizes their browser window.
func (s *ContainerSession) Resize(rows, cols uint16) error {
	return pty.Setsize(s.PTY, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

// IsAlive returns true if the container process is still running.
func (s *ContainerSession) IsAlive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// StopContainer terminates a container by ID.
func (m *Manager) StopContainer(containerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[containerID]
	if !ok {
		return fmt.Errorf("container %s not found", containerID)
	}

	// Send SIGKILL to ensure termination
	if err := syscall.Kill(session.Container.PID, syscall.SIGKILL); err != nil {
		log.Printf("Kill %d: %v (may already be dead)", session.Container.PID, err)
	}

	session.Container.Status = "stopped"
	runtime.SaveMetadata(session.Container)

	// Remove from maps
	delete(m.sessions, containerID)
	for user, id := range m.userMap {
		if id == containerID {
			delete(m.userMap, user)
			break
		}
	}

	return nil
}

// GetSession returns the container session for a given container ID.
func (m *Manager) GetSession(containerID string) (*ContainerSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[containerID]
	return s, ok
}

// GetUserContainerID returns the container ID for a user, if one exists.
func (m *Manager) GetUserContainerID(username string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.userMap[username]
	return id, ok
}

// provisionRootFS copies the template rootfs for a new user.
// Uses cp -a to preserve permissions, ownership, and symlinks.
func provisionRootFS(username string) error {
	destPath := runtime.UserRootFSPath(username)

	// Already provisioned?
	if _, err := os.Stat(destPath); err == nil {
		return nil
	}

	log.Printf("Provisioning rootfs for user %s (this may take a moment)...", username)
	os.MkdirAll(filepath.Dir(destPath), 0755)

	// cp -a preserves all attributes (permissions, ownership, symlinks, etc.)
	cmd := exec.Command("cp", "-a", runtime.TemplateRootFS, destPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("copy rootfs: %w: %s", err, output)
	}

	log.Printf("Rootfs provisioned for %s at %s", username, destPath)
	return nil
}

// generateID creates a unique 8-character hexadecimal ID.
func generateID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
