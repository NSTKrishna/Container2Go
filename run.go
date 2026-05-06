package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// run starts a new container in the background.
//
// Lifecycle:
//   1. Generate a unique container ID
//   2. Create the metadata directory at /tmp/minidocker/<id>/
//   3. Open a log file to capture stdout/stderr
//   4. Re-execute the current binary with "child <id> <cmd> <args...>"
//      inside new Linux namespaces (UTS, PID, Mount)
//   5. Start the child process in the background (non-blocking)
//   6. Record the PID and save metadata as config.json
//   7. Spawn a goroutine to wait for the child and update status on exit
//   8. Print the container ID so the user can reference it later
//
// The container runs detached — the parent process does NOT block.
func run() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: minidocker run <command> [args...]\n")
		os.Exit(1)
	}

	userCmd := strings.Join(os.Args[2:], " ")
	id := generateID()

	fmt.Printf("Starting container %s\n", id)

	// Create the container metadata directory
	containerDir := ContainerDir(id)
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create container directory: %v\n", err)
		os.Exit(1)
	}

	// Open the log file to capture all container output (stdout + stderr)
	logPath := filepath.Join(containerDir, "logs.txt")
	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create log file: %v\n", err)
		os.Exit(1)
	}

	// Build the child command: re-execute ourselves with "child <id> <cmd> <args...>"
	// /proc/self/exe is a symlink to the current running binary — this is the
	// standard trick used by container runtimes to re-exec into new namespaces.
	childArgs := append([]string{"child", id}, os.Args[2:]...)
	cmd := exec.Command("/proc/self/exe", childArgs...)

	// Redirect container output to the log file instead of the terminal.
	// The container runs in the background, so we can't show output directly.
	cmd.Stdin = os.Stdin
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// --- Linux Namespace Flags ---
	// CLONE_NEWUTS:  New UTS namespace — isolates hostname and domain name
	// CLONE_NEWPID:  New PID namespace — container sees its own PID 1
	// CLONE_NEWNS:   New mount namespace — mount/unmount operations are isolated
	//
	// Unshareflags ensures mount events don't propagate to the host.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
		Unshareflags: syscall.CLONE_NEWNS,
	}

	// Start the container process in the background (non-blocking).
	// Unlike cmd.Run(), cmd.Start() returns immediately after the process is created.
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start container: %v\n", err)
		logFile.Close()
		os.Exit(1)
	}

	// Save container metadata to config.json
	c := &Container{
		ID:      id,
		PID:     cmd.Process.Pid,
		Command: userCmd,
		Status:  "running",
		LogFile: logPath,
	}

	if err := SaveMetadata(c); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save metadata: %v\n", err)
		os.Exit(1)
	}

	// Spawn a background goroutine that waits for the container to exit,
	// then updates the status and closes the log file.
	go func() {
		cmd.Wait()
		logFile.Close()

		// Reload metadata (in case it was modified by stop command)
		c, err := LoadMetadata(id)
		if err != nil {
			return
		}
		// Only update to "exited" if not already "stopped"
		if c.Status == "running" {
			c.Status = "exited"
			SaveMetadata(c)
		}
	}()

	fmt.Printf("Container %s started (PID: %d)\n", id, cmd.Process.Pid)

	// Give the background goroutine a moment to handle fast-exiting containers,
	// then exit the parent. For long-running containers, the goroutine will
	// outlive this print but exit when the parent exits. The ps command
	// will detect the dead PID and mark it as exited.
}
