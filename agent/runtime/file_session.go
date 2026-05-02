package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fullmodel/agent/brain"
)

// FileSessionStore persists chat histories as one JSON file per session.
type FileSessionStore struct {
	mu   sync.RWMutex
	root string
}

func NewFileSessionStore(root string) (*FileSessionStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("session root is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &FileSessionStore{root: root}, nil
}

func (s *FileSessionStore) Messages(sessionID string) []brain.Message {
	if s == nil || sessionID == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	messages, _ := s.read(sessionID)
	return messages
}

func (s *FileSessionStore) Append(sessionID string, messages ...brain.Message) {
	if s == nil || sessionID == "" || len(messages) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, _ := s.read(sessionID)
	existing = append(existing, messages...)
	_ = s.write(sessionID, existing)
}

func (s *FileSessionStore) Replace(sessionID string, messages []brain.Message) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := append([]brain.Message(nil), messages...)
	_ = s.write(sessionID, cp)
}

func (s *FileSessionStore) Clear(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(s.path(sessionID))
}

func (s *FileSessionStore) read(sessionID string) ([]brain.Message, error) {
	data, err := os.ReadFile(s.path(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var messages []brain.Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (s *FileSessionStore) write(sessionID string, messages []brain.Message) error {
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(sessionID), data, 0o644)
}

func (s *FileSessionStore) path(sessionID string) string {
	return filepath.Join(s.root, safeID(sessionID)+".json")
}

func safeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
