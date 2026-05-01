package fileop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v2"
)

const (
	defaultLLMConfigFile       = "llm.yaml"
	legacyDefaultLLMConfigFile = "llm_congfig.yaml"
)

// BrainConfigKind marks the three model config groups used by the brain package.
type BrainConfigKind string

const (
	BrainConfigText   BrainConfigKind = "text"
	BrainConfigVision BrainConfigKind = "vision"
	BrainConfigVoice  BrainConfigKind = "voice"
)

// BrainConfigs mirrors config/llm.yaml and keeps each class aligned with brain.Config.
//
// Supported YAML shape:
//
//	text:
//	  api_key: ${DASHSCOPE_API_KEY}
//	  model: qwen-plus
//	  region: cn-beijing
//	  provider: qwen
//	vision:
//	  api_key: ${DASHSCOPE_API_KEY}
//	  model: qwen-vl-plus
//	voice:
//	  api_key: ${DASHSCOPE_API_KEY}
//	  model: cosyvoice-v3-flash
//
// Backward-compatible aliases are also accepted: visual/image for vision,
// speech/audio for voice, and llm/chat for text.
type BrainConfigs struct {
	Text   ModelConfig `yaml:"text"`
	Vision ModelConfig `yaml:"vision"`
	Voice  ModelConfig `yaml:"voice"`
	Image  ModelConfig `yaml:"image_generate"`

	LLM         ModelConfig `yaml:"llm"`
	Chat        ModelConfig `yaml:"chat"`
	Visual      ModelConfig `yaml:"visual"`
	LegacyImage ModelConfig `yaml:"image"`
	Speech      ModelConfig `yaml:"speech"`
	Audio       ModelConfig `yaml:"audio"`
}

// ModelConfig has the same YAML shape as brain.Config, but lives in fileop to
// avoid an import cycle: fileop -> brain -> logging -> fileop.
type ModelConfig struct {
	APIKey       string            `yaml:"api_key" json:"api_key"`
	BaseURL      string            `yaml:"base_url" json:"base_url"`
	Model        string            `yaml:"model" json:"model"`
	Region       string            `yaml:"region" json:"region"`
	Provider     string            `yaml:"provider" json:"provider"`
	APIEndpoints map[string]string `yaml:"endpoints" json:"endpoints"`
}

// LoadBrainConfigs reads config/llm.yaml under the runtime config folder.
// It falls back to the historical config/llm_congfig.yaml when the new file
// has not been created yet.
func LoadBrainConfigs() (*BrainConfigs, error) {
	return LoadBrainConfigsFromFile(defaultLLMConfigFile)
}

// LoadBrainConfigsFromFile reads a YAML config file from the config folder unless filename is absolute.
func LoadBrainConfigsFromFile(filename string) (*BrainConfigs, error) {
	path := configFilePath(filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if strings.TrimSpace(filename) == defaultLLMConfigFile && os.IsNotExist(err) {
			legacyPath := configFilePath(legacyDefaultLLMConfigFile)
			if legacyData, legacyErr := os.ReadFile(legacyPath); legacyErr == nil {
				path = legacyPath
				data = legacyData
				err = nil
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read brain config %s: %w", path, err)
	}
	data = []byte(os.ExpandEnv(string(data)))

	var cfg BrainConfigs
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse brain config %s: %w", path, err)
	}
	cfg.normalize()
	return &cfg, nil
}

// LoadBrainConfig returns one config class from config/llm.yaml.
func LoadBrainConfig(kind BrainConfigKind) (*ModelConfig, error) {
	cfgs, err := LoadBrainConfigs()
	if err != nil {
		return nil, err
	}
	return cfgs.Config(kind)
}

func LoadTextBrainConfig() (*ModelConfig, error) {
	return LoadBrainConfig(BrainConfigText)
}

func LoadVisionBrainConfig() (*ModelConfig, error) {
	return LoadBrainConfig(BrainConfigVision)
}

func LoadVoiceBrainConfig() (*ModelConfig, error) {
	return LoadBrainConfig(BrainConfigVoice)
}

func LoadImageGenerateBrainConfig() (*ModelConfig, error) {
	cfgs, err := LoadBrainConfigs()
	if err != nil {
		return nil, err
	}
	return cloneBrainConfig(cfgs.Image), nil
}

func DefaultLLMConfigPath() string {
	return configFilePath(defaultLLMConfigFile)
}

// Config returns a copy of the requested config class.
func (c *BrainConfigs) Config(kind BrainConfigKind) (*ModelConfig, error) {
	if c == nil {
		return nil, fmt.Errorf("brain configs is nil")
	}
	switch kind {
	case BrainConfigText:
		return cloneBrainConfig(c.Text), nil
	case BrainConfigVision:
		return cloneBrainConfig(c.Vision), nil
	case BrainConfigVoice:
		return cloneBrainConfig(c.Voice), nil
	default:
		return nil, fmt.Errorf("unknown brain config kind %q", kind)
	}
}

func (c *BrainConfigs) normalize() {
	if isZeroBrainConfig(c.Text) {
		c.Text = firstNonZeroBrainConfig(c.LLM, c.Chat)
	}
	if isZeroBrainConfig(c.Vision) {
		c.Vision = firstNonZeroBrainConfig(c.Visual, c.LegacyImage)
	}
	if isZeroBrainConfig(c.Voice) {
		c.Voice = firstNonZeroBrainConfig(c.Speech, c.Audio)
	}
	if isZeroBrainConfig(c.Image) {
		c.Image = firstNonZeroBrainConfig(c.LegacyImage, c.Vision)
		if !isZeroBrainConfig(c.Image) && isZeroBrainConfig(c.LegacyImage) {
			c.Image.BaseURL = ""
			c.Image.Model = ""
		}
	}

	applyBrainConfigDefaults(&c.Text, "qwen", "cn-beijing", "qwen-plus")
	applyBrainConfigDefaults(&c.Vision, "qwen", "cn-beijing", "qwen-vl-plus")
	applyBrainConfigDefaults(&c.Voice, "qwen", "cn-beijing", "cosyvoice-v3-flash")
	applyBrainConfigDefaults(&c.Image, "qwen", "cn-beijing", "qwen-image-2.0-pro")
}

func applyBrainConfigDefaults(cfg *ModelConfig, provider, region, model string) {
	if cfg.Provider == "" {
		cfg.Provider = provider
	}
	if cfg.Region == "" {
		cfg.Region = region
	}
	if cfg.Model == "" {
		cfg.Model = model
	}
}

func firstNonZeroBrainConfig(candidates ...ModelConfig) ModelConfig {
	for _, candidate := range candidates {
		if !isZeroBrainConfig(candidate) {
			return candidate
		}
	}
	return ModelConfig{}
}

func isZeroBrainConfig(cfg ModelConfig) bool {
	return strings.TrimSpace(cfg.APIKey) == "" &&
		strings.TrimSpace(cfg.BaseURL) == "" &&
		strings.TrimSpace(cfg.Model) == "" &&
		strings.TrimSpace(cfg.Region) == "" &&
		cfg.Provider == "" &&
		len(cfg.APIEndpoints) == 0
}

func cloneBrainConfig(cfg ModelConfig) *ModelConfig {
	out := cfg
	if cfg.APIEndpoints != nil {
		out.APIEndpoints = make(map[string]string, len(cfg.APIEndpoints))
		for key, value := range cfg.APIEndpoints {
			out.APIEndpoints[key] = value
		}
	}
	return &out
}

func configFilePath(filename string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		name = defaultLLMConfigFile
	}
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	if strings.HasPrefix(filepath.Clean(name), "config"+string(filepath.Separator)) {
		return ResolvePath(name)
	}
	return ResolvePath(filepath.Join("config", name))
}
