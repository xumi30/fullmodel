package runtime

import (
	"testing"

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
	store, err := NewArtifactStore(t.TempDir())
	require.NoError(t, err)

	artifact, err := store.Save([]byte("hello"), "text/plain")
	require.NoError(t, err)
	require.NotEmpty(t, artifact.ID)
	require.Equal(t, int64(5), artifact.Size)

	got, ok := store.Get(artifact.ID)
	require.True(t, ok)
	require.Equal(t, artifact.Path, got.Path)
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
