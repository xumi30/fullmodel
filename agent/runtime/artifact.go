package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Artifact struct {
	ID        string     `json:"id"`
	MimeType  string     `json:"mime_type"`
	Path      string     `json:"path"`
	Size      int64      `json:"size"`
	SessionID string     `json:"session_id,omitempty"`
	TaskID    string     `json:"task_id,omitempty"`
	Source    string     `json:"source,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type ArtifactMeta struct {
	SessionID string
	TaskID    string
	Source    string
	ExpiresAt *time.Time
}

type ArtifactStoreOptions struct {
	Root     string
	MaxBytes int64
	TTL      time.Duration
}

type ArtifactStore struct {
	mu       sync.RWMutex
	root     string
	index    string
	maxBytes int64
	ttl      time.Duration
	data     map[string]Artifact
}

func NewArtifactStore(root string) (*ArtifactStore, error) {
	return NewArtifactStoreWithOptions(ArtifactStoreOptions{Root: root})
}

func NewArtifactStoreWithOptions(opts ArtifactStoreOptions) (*ArtifactStore, error) {
	if strings.TrimSpace(opts.Root) == "" {
		return nil, fmt.Errorf("artifact root is empty")
	}
	if err := os.MkdirAll(opts.Root, 0o755); err != nil {
		return nil, err
	}
	store := &ArtifactStore{
		root:     opts.Root,
		index:    filepath.Join(opts.Root, ".index.json"),
		maxBytes: opts.MaxBytes,
		ttl:      opts.TTL,
		data:     make(map[string]Artifact),
	}
	if err := store.loadIndex(); err != nil {
		return nil, err
	}
	if err := store.CleanupExpired(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *ArtifactStore) Save(data []byte, mimeType string) (Artifact, error) {
	return s.SaveWithMeta(data, mimeType, ArtifactMeta{})
}

func (s *ArtifactStore) SaveWithMeta(data []byte, mimeType string, meta ArtifactMeta) (Artifact, error) {
	if s == nil {
		return Artifact{}, fmt.Errorf("artifact store is nil")
	}
	if len(data) == 0 {
		return Artifact{}, fmt.Errorf("artifact data is empty")
	}
	if s.maxBytes > 0 && int64(len(data)) > s.maxBytes {
		return Artifact{}, fmt.Errorf("artifact is too large: %d > %d bytes", len(data), s.maxBytes)
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = http.DetectContentType(data)
	}
	id, err := randomID("art")
	if err != nil {
		return Artifact{}, err
	}
	path := filepath.Join(s.root, id+extensionForMime(mimeType))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return Artifact{}, err
	}
	now := time.Now().UTC()
	expiresAt := meta.ExpiresAt
	if expiresAt == nil && s.ttl > 0 {
		expire := now.Add(s.ttl)
		expiresAt = &expire
	}
	artifact := Artifact{
		ID:        id,
		MimeType:  mimeType,
		Path:      path,
		Size:      int64(len(data)),
		SessionID: strings.TrimSpace(meta.SessionID),
		TaskID:    strings.TrimSpace(meta.TaskID),
		Source:    strings.TrimSpace(meta.Source),
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = artifact
	return artifact, s.saveIndexLocked()
}

func (s *ArtifactStore) Get(id string) (Artifact, bool) {
	if s == nil {
		return Artifact{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.data[id]
	return artifact, ok
}

func (s *ArtifactStore) List() []Artifact {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Artifact, 0, len(s.data))
	for _, artifact := range s.data {
		items = append(items, artifact)
	}
	return items
}

func (s *ArtifactStore) CleanupExpired() error {
	if s == nil {
		return nil
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id, artifact := range s.data {
		if artifact.ExpiresAt != nil && !artifact.ExpiresAt.After(now) {
			_ = os.Remove(artifact.Path)
			delete(s.data, id)
			changed = true
			continue
		}
		if _, err := os.Stat(artifact.Path); err != nil {
			delete(s.data, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveIndexLocked()
}

func (s *ArtifactStore) loadIndex() error {
	data, err := os.ReadFile(s.index)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var artifacts []Artifact
	if err := json.Unmarshal(data, &artifacts); err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if artifact.ID == "" || artifact.Path == "" {
			continue
		}
		s.data[artifact.ID] = artifact
	}
	return nil
}

func (s *ArtifactStore) saveIndexLocked() error {
	items := make([]Artifact, 0, len(s.data))
	for _, artifact := range s.data {
		items = append(items, artifact)
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.index, data, 0o644)
}

func randomID(prefix string) (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(raw[:]), nil
}

func RandomPublicID(prefix string) (string, error) {
	return randomID(prefix)
}

func extensionForMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	default:
		return ".bin"
	}
}
