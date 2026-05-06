package main

import (
	"crypto/rand"
	"fmt"
)

// must panics if an error is encountered.
// Used for critical operations where failure is unrecoverable
// (e.g., mounting filesystems, setting up namespaces).
func must(err error) {
	if err != nil {
		panic(err)
	}
}

// generateID creates a unique 8-character hexadecimal container ID.
// Uses crypto/rand for randomness so IDs won't collide even across reboots.
func generateID() string {
	b := make([]byte, 4) // 4 bytes = 8 hex characters
	_, err := rand.Read(b)
	if err != nil {
		panic(fmt.Sprintf("failed to generate container ID: %v", err))
	}
	return fmt.Sprintf("%x", b)
}
