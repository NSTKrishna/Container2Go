package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// baseDir is the root directory where all container metadata is stored.
// Each container gets its own subdirectory: /tmp/minidocker/<container-id>/
const baseDir = "/tmp/minidocker"

// Container holds the metadata for a single container instance.
// This struct is serialized to JSON and stored as config.json inside
// the container's metadata directory.
type Container struct {
	ID      string `json:"id"`      // Unique 8-char hex identifier
	PID     int    `json:"pid"`     // Host PID of the container's init process
	Command string `json:"command"` // The command running inside the container
	Status  string `json:"status"`  // One of: "running", "exited", "stopped"
	LogFile string `json:"log_file"` // Absolute path to the container's log file
}

// ContainerDir returns the absolute path to a container's metadata directory.
// Example: /tmp/minidocker/a1b2c3d4/
func ContainerDir(id string) string {
	return filepath.Join(baseDir, id)
}

// SaveMetadata writes the Container struct to config.json inside
// the container's metadata directory. Creates the directory if needed.
func SaveMetadata(c *Container) error {
	dir := ContainerDir(c.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create container dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal container metadata: %w", err)
	}

	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config.json: %w", err)
	}

	return nil
}

// LoadMetadata reads and deserializes a container's config.json file.
// Returns an error if the container directory or config file doesn't exist.
func LoadMetadata(id string) (*Container, error) {
	configPath := filepath.Join(ContainerDir(id), "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config for container %s: %w", id, err)
	}

	var c Container
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("failed to parse config for container %s: %w", id, err)
	}

	return &c, nil
}

// ListContainers scans the base metadata directory and returns metadata
// for every container that has a valid config.json file.
// Containers with corrupted or missing config files are silently skipped.
func ListContainers() ([]*Container, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No containers have been created yet
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list container directory: %w", err)
	}

	var containers []*Container
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		c, err := LoadMetadata(entry.Name())
		if err != nil {
			// Skip containers with broken metadata
			continue
		}

		containers = append(containers, c)
	}

	return containers, nil
}
