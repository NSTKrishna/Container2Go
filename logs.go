package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// logs prints the stdout/stderr output captured from a container.
//
// How it works:
//   1. Validate that a container ID was provided
//   2. Verify the container exists by loading its metadata
//   3. Read the logs.txt file from the container's metadata directory
//   4. Print the contents to stdout
//
// The log file captures everything written to stdout and stderr by the
// container's child process. This includes both the user command's output
// and any setup messages from the child() function.
func logs() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: minidocker logs <container-id>\n")
		os.Exit(1)
	}

	id := os.Args[2]

	// Verify the container exists
	_, err := LoadMetadata(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: container %s not found\n", id)
		os.Exit(1)
	}

	// Read and print the log file
	logPath := filepath.Join(ContainerDir(id), "logs.txt")
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("No logs available for container %s.\n", id)
			return
		}
		fmt.Fprintf(os.Stderr, "Error reading logs: %v\n", err)
		os.Exit(1)
	}

	if len(data) == 0 {
		fmt.Printf("No logs available for container %s.\n", id)
		return
	}

	fmt.Print(string(data))
}
