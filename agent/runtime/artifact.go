package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Artifact struct {
	ID        string    `json:"id"`
	MimeType  string    `json:"mime_type"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

type ArtifactStore struct {
	mu   sync.RWMutex
	root string
	data map[string]Artifact
}

func NewArtifactStore(root string) (*ArtifactStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("artifact root is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &ArtifactStore{
		root: root,
		data: make(map[string]Artifact),
	}, nil
}

func (s *ArtifactStore) Save(data []byte, mimeType string) (Artifact, error) {
	if s == nil {
		return Artifact{}, fmt.Errorf("artifact store is nil")
	}
	if len(data) == 0 {
		return Artifact{}, fmt.Errorf("artifact data is empty")
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
	artifact := Artifact{
		ID:        id,
		MimeType:  mimeType,
		Path:      path,
		Size:      int64(len(data)),
		CreatedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = artifact
	return artifact, nil
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
