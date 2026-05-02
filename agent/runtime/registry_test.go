package runtime

import (
	"testing"
	"time"

	"fullmodel/agent/brain"
	"fullmodel/processmessage"
	"fullmodel/utils/fileop"

	"github.com/stretchr/testify/require"
)

func TestNewRegistryFromConfigs(t *testing.T) {
	cfgs := &fileop.BrainConfigs{
		Defaults: fileop.ModelConfig{APIKey: "key"},
		Brains: map[fileop.BrainConfigKind]fileop.ModelConfig{
			fileop.BrainConfigText: {Model: "qwen-plus"},
		},
	}

	registry, err := NewRegistryFromConfigs(cfgs)
	require.NoError(t, err)
	require.NotNil(t, registry)

	_, ok := registry.SelectBrain(processmessage.KindText)
	require.True(t, ok)
	_, ok = registry.SelectBrain(processmessage.KindImageGenerate)
	require.True(t, ok)
	require.NotEmpty(t, registry.Capabilities())
}

func TestSessionStoreCopiesMessages(t *testing.T) {
	store := NewSessionStore()
	store.Append("s1", brain.NewUserMessage("hello"))

	messages := store.Messages("s1")
	require.Len(t, messages, 1)
	messages[0].Content = "changed"

	again := store.Messages("s1")
	require.Equal(t, "hello", again[0].Content)
}

func TestArtifactStoreSaveAndGet(t *testing.T) {
	root := t.TempDir()
	store, err := NewArtifactStore(root)
	require.NoError(t, err)

	artifact, err := store.Save([]byte("hello"), "text/plain")
	require.NoError(t, err)
	require.NotEmpty(t, artifact.ID)
	require.Equal(t, int64(5), artifact.Size)

	got, ok := store.Get(artifact.ID)
	require.True(t, ok)
	require.Equal(t, artifact.Path, got.Path)

	reopened, err := NewArtifactStore(root)
	require.NoError(t, err)
	reopenedArtifact, ok := reopened.Get(artifact.ID)
	require.True(t, ok)
	require.Equal(t, artifact.Path, reopenedArtifact.Path)
}

func TestArtifactStoreLifecycle(t *testing.T) {
	root := t.TempDir()
	store, err := NewArtifactStoreWithOptions(ArtifactStoreOptions{
		Root:     root,
		MaxBytes: 16,
		TTL:      time.Hour,
	})
	require.NoError(t, err)

	artifact, err := store.SaveWithMeta([]byte("hello"), "text/plain", ArtifactMeta{
		SessionID: "s1",
		TaskID:    "t1",
		Source:    "test",
	})
	require.NoError(t, err)
	require.Equal(t, "s1", artifact.SessionID)
	require.NotNil(t, artifact.ExpiresAt)

	reopened, err := NewArtifactStore(root)
	require.NoError(t, err)
	got, ok := reopened.Get(artifact.ID)
	require.True(t, ok)
	require.Equal(t, "t1", got.TaskID)

	_, err = store.Save([]byte("this payload is too large"), "text/plain")
	require.Error(t, err)

	expired := time.Now().UTC().Add(-time.Minute)
	_, err = store.SaveWithMeta([]byte("bye"), "text/plain", ArtifactMeta{ExpiresAt: &expired})
	require.NoError(t, err)
	require.NoError(t, store.CleanupExpired())
	require.Len(t, store.List(), 1)
}

func TestFileSessionStorePersistsMessages(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileSessionStore(root)
	require.NoError(t, err)
	store.Append("demo/session", brain.NewUserMessage("hello"))

	reopened, err := NewFileSessionStore(root)
	require.NoError(t, err)
	messages := reopened.Messages("demo/session")
	require.Len(t, messages, 1)
	require.Equal(t, "hello", messages[0].Content)

	reopened.Clear("demo/session")
	require.Empty(t, reopened.Messages("demo/session"))
}
