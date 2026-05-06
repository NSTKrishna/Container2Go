// Package runtime provides low-level Linux container primitives.
//
// It handles namespace setup, chroot filesystem isolation, cgroup resource
// limits, and container metadata persistence. This package is used by both
// the HTTP server and the standalone CLI.
package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Directory layout for container metadata and rootfs storage.
const (
	// BaseDir is the root of all minidocker state on the host.
	BaseDir = "/tmp/minidocker"

	// ContainersDir stores per-container metadata (config.json, logs.txt).
	ContainersDir = "/tmp/minidocker/containers"

	// RootFSBaseDir stores per-user root filesystems cloned from the template.
	RootFSBaseDir = "/tmp/minidocker/rootfs"

	// TemplateRootFS is the base Ubuntu rootfs used as a template.
	// Each user gets a copy of this directory as their container's root.
	// Create with: sudo debootstrap focal /home/ubuntu/ubuntufs
	TemplateRootFS = "/home/ubuntu/ubuntufs"
)

// Container holds the metadata for a single container instance.
// Serialized to JSON and persisted as config.json in the container directory.
type Container struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	PID       int       `json:"pid"`
	Command   string    `json:"command"`
	Status    string    `json:"status"`    // "running", "exited", "stopped"
	RootFS    string    `json:"rootfs"`    // Absolute path to this container's rootfs
	LogFile   string    `json:"log_file"`  // Absolute path to logs.txt
	CreatedAt time.Time `json:"created_at"`
}

// ContainerPath returns the metadata directory for a container.
// Example: /tmp/minidocker/containers/abc123/
func ContainerPath(id string) string {
	return filepath.Join(ContainersDir, id)
}

// UserRootFSPath returns the rootfs directory for a user.
// Example: /tmp/minidocker/rootfs/alice/
func UserRootFSPath(username string) string {
	return filepath.Join(RootFSBaseDir, username)
}

// SaveMetadata writes the Container struct to config.json.
func SaveMetadata(c *Container) error {
	dir := ContainerPath(c.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create container dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)
}

// LoadMetadata reads a container's config.json.
func LoadMetadata(id string) (*Container, error) {
	data, err := os.ReadFile(filepath.Join(ContainerPath(id), "config.json"))
	if err != nil {
		return nil, fmt.Errorf("read config for %s: %w", id, err)
	}

	var c Container
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config for %s: %w", id, err)
	}
	return &c, nil
}

// ListContainers returns metadata for all containers.
func ListContainers() ([]*Container, error) {
	entries, err := os.ReadDir(ContainersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var containers []*Container
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := LoadMetadata(e.Name())
		if err != nil {
			continue
		}
		containers = append(containers, c)
	}
	return containers, nil
}

// ListUserContainers returns containers owned by a specific user.
func ListUserContainers(username string) ([]*Container, error) {
	all, err := ListContainers()
	if err != nil {
		return nil, err
	}

	var result []*Container
	for _, c := range all {
		if c.UserID == username {
			result = append(result, c)
		}
	}
	return result, nil
}
