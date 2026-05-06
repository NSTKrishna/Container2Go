// Package auth provides in-memory user authentication and session management.
//
// This is an educational implementation using hardcoded users and in-memory
// session storage. In production, you would use a database, bcrypt password
// hashing, and JWT or OAuth tokens.
package auth

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Service manages users and their active sessions.
type Service struct {
	users    map[string]string  // username → password (plaintext for demo)
	sessions map[string]Session // token → session
	mu       sync.RWMutex
}

// Session represents an authenticated user session.
type Session struct {
	Username  string
	Token     string
	ExpiresAt time.Time
}

// New creates an auth service with pre-seeded demo users.
func New() *Service {
	return &Service{
		users: map[string]string{
			"alice": "password",
			"bob":   "password",
			"admin": "admin",
		},
		sessions: make(map[string]Session),
	}
}

// Login validates credentials and creates a session.
// Returns a session token on success.
func (s *Service) Login(username, password string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	expected, ok := s.users[username]
	if !ok || expected != password {
		return "", fmt.Errorf("invalid credentials")
	}

	token := generateToken()
	s.sessions[token] = Session{
		Username:  username,
		Token:     token,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	return token, nil
}

// ValidateSession checks if a token is valid and returns the username.
func (s *Service) ValidateSession(token string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[token]
	if !ok {
		return "", fmt.Errorf("invalid session")
	}
	if time.Now().After(session.ExpiresAt) {
		return "", fmt.Errorf("session expired")
	}
	return session.Username, nil
}

// Logout removes a session.
func (s *Service) Logout(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// GetUsernameFromRequest extracts and validates the session from an HTTP request.
func (s *Service) GetUsernameFromRequest(r *http.Request) (string, error) {
	cookie, err := r.Cookie("session_token")
	if err != nil {
		return "", fmt.Errorf("no session cookie")
	}
	return s.ValidateSession(cookie.Value)
}

// generateToken creates a cryptographically random 32-char hex token.
func generateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
