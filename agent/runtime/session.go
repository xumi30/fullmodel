package runtime

import (
	"github.com/xumi30/fullmodel/agent/brain"
	"sync"
)

type SessionMemory interface {
	Messages(sessionID string) []brain.Message
	Append(sessionID string, messages ...brain.Message)
	Replace(sessionID string, messages []brain.Message)
	Clear(sessionID string)
}

// SessionStore keeps short-lived chat histories for interactive runtimes.
// Persistent memory can implement the same shape later without changing callers.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string][]brain.Message
}

func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: make(map[string][]brain.Message)}
}

func (s *SessionStore) Messages(sessionID string) []brain.Message {
	if s == nil || sessionID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.sessions[sessionID]
	out := make([]brain.Message, len(src))
	copy(out, src)
	return out
}

func (s *SessionStore) Append(sessionID string, messages ...brain.Message) {
	if s == nil || sessionID == "" || len(messages) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = append(s.sessions[sessionID], messages...)
}

func (s *SessionStore) Replace(sessionID string, messages []brain.Message) {
	if s == nil || sessionID == "" {
		return
	}
	cp := make([]brain.Message, len(messages))
	copy(cp, messages)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = cp
}

func (s *SessionStore) Clear(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}
