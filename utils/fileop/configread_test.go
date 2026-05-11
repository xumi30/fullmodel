package fileop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadBrainConfigs(t *testing.T) {
	t.Run("loads simple defaults plus brains", func(t *testing.T) {
		tmpDir := t.TempDir()
		restoreRoot := setRuntimeRootForTest(t, tmpDir)
		defer restoreRoot()

		writeConfig(t, tmpDir, `
defaults:
  api_key: test_key
  provider: qwen
  region: cn-beijing
brains:
  text:
    model: qwen-plus
  vision:
    model: qwen-vl-plus
  voice:
    model: cosyvoice-v3-flash
  voice_realtime_ws:
    model: qwen3-tts-flash-realtime
  image:
    model: qwen-image-2.0-pro
`)

		cfgs, err := LoadBrainConfigs()
		require.NoError(t, err)

		text, err := cfgs.Config(BrainConfigText)
		require.NoError(t, err)
		require.Equal(t, "test_key", text.APIKey)
		require.Equal(t, "qwen", text.Provider)
		require.Equal(t, "cn-beijing", text.Region)
		require.Equal(t, "qwen-plus", text.Model)

		image, err := cfgs.Config(BrainConfigImage)
		require.NoError(t, err)
		require.Equal(t, "qwen-image-2.0-pro", image.Model)

		ws, err := cfgs.Config(BrainConfigVoiceRealtimeWS)
		require.NoError(t, err)
		require.Equal(t, "qwen3-tts-flash-realtime", ws.Model)

		asr, err := cfgs.Config(BrainConfigASR)
		require.NoError(t, err)
		require.Equal(t, "test_key", asr.APIKey)
		require.Equal(t, "fun-asr-realtime", asr.Model)
	})

	t.Run("voice_realtime_ws uses built-in default when omitted from yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		restoreRoot := setRuntimeRootForTest(t, tmpDir)
		defer restoreRoot()

		writeConfig(t, tmpDir, `
defaults:
  api_key: test_key
  provider: qwen
  region: cn-beijing
brains:
  text:
    model: qwen-plus
`)

		cfgs, err := LoadBrainConfigs()
		require.NoError(t, err)

		ws, err := cfgs.Config(BrainConfigVoiceRealtimeWS)
		require.NoError(t, err)
		require.Equal(t, DefaultVoiceRealtimeWSModel, ws.Model)
	})

	t.Run("expands environment variables", func(t *testing.T) {
		t.Setenv("TEST_API_KEY", "env_key")
		tmpDir := t.TempDir()
		restoreRoot := setRuntimeRootForTest(t, tmpDir)
		defer restoreRoot()

		writeConfig(t, tmpDir, `
defaults:
  api_key: ${TEST_API_KEY}
brains:
  text:
    model: test-model
`)

		cfg, err := LoadTextBrainConfig()
		require.NoError(t, err)
		require.Equal(t, "env_key", cfg.APIKey)
		require.Equal(t, "test-model", cfg.Model)
	})

	t.Run("loads profiles", func(t *testing.T) {
		t.Setenv("TEST_API_KEY", "profile_key")
		tmpDir := t.TempDir()
		restoreRoot := setRuntimeRootForTest(t, tmpDir)
		defer restoreRoot()

		writeConfig(t, tmpDir, `
defaults:
  profile: qwen
profiles:
  qwen:
    api_key: ${TEST_API_KEY}
    provider: qwen
    region: cn-beijing
  local:
    base_url: http://127.0.0.1:11434/v1
    provider: openai
brains:
  text:
    profile: local
    model: local-model
  image:
    model: qwen-image
`)

		cfgs, err := LoadBrainConfigs()
		require.NoError(t, err)

		text, err := cfgs.Config(BrainConfigText)
		require.NoError(t, err)
		require.Equal(t, "local-model", text.Model)
		require.Equal(t, "openai", text.Provider)
		require.Equal(t, "http://127.0.0.1:11434/v1", text.BaseURL)

		image, err := cfgs.Config(BrainConfigImage)
		require.NoError(t, err)
		require.Equal(t, "profile_key", image.APIKey)
		require.Equal(t, "qwen", image.Provider)
	})

	t.Run("missing file", func(t *testing.T) {
		tmpDir := t.TempDir()
		restoreRoot := setRuntimeRootForTest(t, tmpDir)
		defer restoreRoot()

		cfg, err := LoadBrainConfigs()
		require.Error(t, err)
		require.Nil(t, cfg)
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("malformed yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		restoreRoot := setRuntimeRootForTest(t, tmpDir)
		defer restoreRoot()

		writeConfig(t, tmpDir, "defaults:\n  api_key: ok\nbad")

		cfg, err := LoadBrainConfigs()
		require.Error(t, err)
		require.Nil(t, cfg)
		require.Contains(t, err.Error(), "parse brain config")
	})
}

func TestBrainConfigsConfig(t *testing.T) {
	cfgs := &BrainConfigs{
		Defaults: ModelConfig{
			APIKey:   "shared_key",
			Provider: "openai",
			Region:   "us-east-1",
		},
		Brains: map[BrainConfigKind]ModelConfig{
			BrainConfigText: {
				Model:   "gpt-4o",
				BaseURL: "https://example.com/v1",
				APIEndpoints: map[string]string{
					"chat": "https://example.com/v1/chat/completions",
				},
			},
			BrainConfigVoice: {
				APIKey: "voice_key",
				Model:  "cosyvoice-v3-flash",
			},
			BrainConfigVoiceRealtimeWS: {
				Model: "qwen3-tts-flash-realtime",
			},
		},
	}

	text, err := cfgs.Config(BrainConfigText)
	require.NoError(t, err)
	require.Equal(t, "shared_key", text.APIKey)
	require.Equal(t, "openai", text.Provider)
	require.Equal(t, "gpt-4o", text.Model)
	require.Equal(t, "https://example.com/v1", text.BaseURL)
	require.Equal(t, "https://example.com/v1/chat/completions", text.APIEndpoints["chat"])

	voice, err := cfgs.Config(BrainConfigVoice)
	require.NoError(t, err)
	require.Equal(t, "voice_key", voice.APIKey)
	require.Equal(t, "openai", voice.Provider)
	require.Equal(t, "cosyvoice-v3-flash", voice.Model)

	voiceWs, err := cfgs.Config(BrainConfigVoiceRealtimeWS)
	require.NoError(t, err)
	require.Equal(t, "shared_key", voiceWs.APIKey)
	require.Equal(t, "qwen3-tts-flash-realtime", voiceWs.Model)

	asr, err := cfgs.Config(BrainConfigASR)
	require.NoError(t, err)
	require.Equal(t, "shared_key", asr.APIKey)
	require.Equal(t, "fun-asr-realtime", asr.Model)

	vision, err := cfgs.Config(BrainConfigVision)
	require.NoError(t, err)
	require.Equal(t, "shared_key", vision.APIKey)
	require.Equal(t, "qwen-vl-plus", vision.Model)

	_, err = cfgs.Config(BrainConfigKind("unknown"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown brain config kind")

	var nilConfigs *BrainConfigs
	_, err = nilConfigs.Config(BrainConfigText)
	require.Error(t, err)
	require.Contains(t, err.Error(), "brain configs is nil")
}

func TestLoadSpecificBrainConfigs(t *testing.T) {
	tmpDir := t.TempDir()
	restoreRoot := setRuntimeRootForTest(t, tmpDir)
	defer restoreRoot()

	writeConfig(t, tmpDir, `
defaults:
  api_key: shared_key
brains:
  text:
    model: qwen-plus
  vision:
    model: qwen-vl-plus
  voice:
    model: cosyvoice-v3-flash
  voice_realtime_ws:
    model: qwen3-tts-flash-realtime
  image:
    model: qwen-image-2.0-pro
  omni:
    model: qwen3.5-omni-plus
`)

	text, err := LoadTextBrainConfig()
	require.NoError(t, err)
	require.Equal(t, "qwen-plus", text.Model)

	vision, err := LoadVisionBrainConfig()
	require.NoError(t, err)
	require.Equal(t, "qwen-vl-plus", vision.Model)

	voice, err := LoadVoiceBrainConfig()
	require.NoError(t, err)
	require.Equal(t, "cosyvoice-v3-flash", voice.Model)

	image, err := LoadImageGenerateBrainConfig()
	require.NoError(t, err)
	require.Equal(t, "qwen-image-2.0-pro", image.Model)

	omni, err := LoadOmniBrainConfig()
	require.NoError(t, err)
	require.Equal(t, "qwen3.5-omni-plus", omni.Model)

	voiceWs, err := LoadVoiceRealtimeWSBrainConfig()
	require.NoError(t, err)
	require.Equal(t, "qwen3-tts-flash-realtime", voiceWs.Model)

	asr, err := LoadASRBrainConfig()
	require.NoError(t, err)
	require.Equal(t, "shared_key", asr.APIKey)
	require.Equal(t, "fun-asr-realtime", asr.Model)
}

func TestConfigFilePath(t *testing.T) {
	tmpDir := t.TempDir()
	restoreRoot := setRuntimeRootForTest(t, tmpDir)
	defer restoreRoot()

	require.Equal(t, filepath.Join(tmpDir, "config", "llm.yaml"), configFilePath(""))
	require.Equal(t, filepath.Join(tmpDir, "config", "llm.yaml"), configFilePath("llm.yaml"))
	require.Equal(t, filepath.Join(tmpDir, "config", "llm.yaml"), configFilePath("config/llm.yaml"))
	require.Equal(t, "/tmp/custom.yaml", configFilePath("/tmp/custom.yaml"))
}

func TestCloneBrainConfig(t *testing.T) {
	original := ModelConfig{
		APIKey:       "test_key",
		Model:        "test-model",
		APIEndpoints: map[string]string{"chat": "old"},
	}

	clone := cloneBrainConfig(original)
	require.Equal(t, original.APIKey, clone.APIKey)
	require.Equal(t, original.Model, clone.Model)

	original.APIEndpoints["chat"] = "new"
	require.Equal(t, "old", clone.APIEndpoints["chat"])
}

func writeConfig(t *testing.T, root, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config", "llm.yaml"), []byte(content), 0o644))
}

func setRuntimeRootForTest(t *testing.T, root string) func() {
	t.Helper()
	oldRoot, hadOldRoot := os.LookupEnv("peopleAgent_HOME")
	require.NoError(t, os.Setenv("peopleAgent_HOME", root))
	return func() {
		if hadOldRoot {
			require.NoError(t, os.Setenv("peopleAgent_HOME", oldRoot))
		} else {
			require.NoError(t, os.Unsetenv("peopleAgent_HOME"))
		}
	}
}
