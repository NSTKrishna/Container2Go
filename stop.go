package main

import (
	"fmt"
	"os"
	"syscall"
)

// stop terminates a running container by its ID.
//
// How it works:
//   1. Validate that a container ID was provided
//   2. Load the container's metadata from config.json
//   3. Check if the container is still running
//   4. Send SIGKILL to the container's PID to forcefully terminate it
//      - SIGKILL (signal 9) cannot be caught or ignored by the process
//      - This ensures the container is always terminated
//   5. Update the container's status to "stopped" in config.json
//
// Edge cases handled:
//   - Container ID doesn't exist → error message
//   - Container already exited → inform user, update status
//   - Container already stopped → inform user
//   - Kill fails (process already dead) → still mark as stopped
func stop() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: minidocker stop <container-id>\n")
		os.Exit(1)
	}

	id := os.Args[2]

	// Load container metadata
	c, err := LoadMetadata(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: container %s not found\n", id)
		os.Exit(1)
	}

	// Check current status
	if c.Status == "stopped" {
		fmt.Printf("Container %s is already stopped.\n", id)
		return
	}

	if c.Status == "exited" {
		fmt.Printf("Container %s has already exited.\n", id)
		return
	}

	// Send SIGKILL to terminate the container process.
	// We use SIGKILL instead of SIGTERM because the containerized process
	// might not have a signal handler, and we want guaranteed termination.
	err = syscall.Kill(c.PID, syscall.SIGKILL)
	if err != nil {
		// Process might already be dead — that's okay, we still mark it stopped.
		fmt.Printf("Note: process %d may have already exited: %v\n", c.PID, err)
	}

	// Update status to "stopped"
	c.Status = "stopped"
	if err := SaveMetadata(c); err != nil {
		fmt.Fprintf(os.Stderr, "Error updating metadata: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Container %s stopped.\n", id)
}
