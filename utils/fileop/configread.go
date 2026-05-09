package fileop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v2"
)

const defaultLLMConfigFile = "llm.yaml"

// BrainConfigKind marks a model capability configured in config/llm.yaml.
type BrainConfigKind string

const (
	BrainConfigText   BrainConfigKind = "text"
	BrainConfigVision BrainConfigKind = "vision"
	BrainConfigVoice  BrainConfigKind = "voice"
	BrainConfigImage  BrainConfigKind = "image"
	// BrainConfigOmni 全模态理解（百炼 Qwen-Omni 等 compatible-mode）
	BrainConfigOmni BrainConfigKind = "omni"
)

var brainConfigDefaults = map[BrainConfigKind]ModelConfig{
	BrainConfigText: {
		Provider: "qwen",
		Region:   "cn-beijing",
		Model:    "qwen-plus",
	},
	BrainConfigVision: {
		Provider: "qwen",
		Region:   "cn-beijing",
		Model:    "qwen-vl-plus",
	},
	BrainConfigVoice: {
		Provider: "qwen",
		Region:   "cn-beijing",
		Model:    "cosyvoice-v3-flash",
	},
	BrainConfigImage: {
		Provider: "qwen",
		Region:   "cn-beijing",
		Model:    "qwen-image-2.0-pro",
	},
	BrainConfigOmni: {
		Provider: "qwen",
		Region:   "cn-beijing",
		Model:    "qwen3.5-omni-plus",
	},
}

// BrainConfigs mirrors the simple config/llm.yaml shape:
//
//	defaults:
//	  profile: qwen
//	profiles:
//	  qwen:
//	    api_key: ${DASHSCOPE_API_KEY}
//	    provider: qwen
//	    region: cn-beijing
//	brains:
//	  text:
//	    model: qwen-plus
//	  vision:
//	    model: qwen-vl-plus
//	  voice:
//	    model: cosyvoice-v3-flash
//	  image:
//	    model: qwen-image-2.0-pro
//	  omni:
//	    model: qwen3.5-omni-plus
type BrainConfigs struct {
	Defaults ModelConfig                     `yaml:"defaults" json:"defaults"`
	Profiles map[string]ModelConfig          `yaml:"profiles" json:"profiles"`
	Brains   map[BrainConfigKind]ModelConfig `yaml:"brains" json:"brains"`
}

// ModelConfig has the same YAML shape as brain.Config, but lives in fileop to
// avoid an import cycle: fileop -> brain -> logging -> fileop.
type ModelConfig struct {
	Profile      string            `yaml:"profile" json:"profile"`
	APIKey       string            `yaml:"api_key" json:"api_key"`
	BaseURL      string            `yaml:"base_url" json:"base_url"`
	Model        string            `yaml:"model" json:"model"`
	Region       string            `yaml:"region" json:"region"`
	Provider     string            `yaml:"provider" json:"provider"`
	APIEndpoints map[string]string `yaml:"endpoints" json:"endpoints"`
}

// LoadBrainConfigs reads config/llm.yaml under the runtime config folder.
func LoadBrainConfigs() (*BrainConfigs, error) {
	return LoadBrainConfigsFromFile(defaultLLMConfigFile)
}

// LoadBrainConfigsFromFile reads a YAML config file from the config folder unless filename is absolute.
func LoadBrainConfigsFromFile(filename string) (*BrainConfigs, error) {
	path := configFilePath(filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read brain config %s: %w", path, err)
	}

	var cfg BrainConfigs
	expanded := []byte(os.ExpandEnv(string(data)))
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
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
	return LoadBrainConfig(BrainConfigImage)
}

func LoadOmniBrainConfig() (*ModelConfig, error) {
	return LoadBrainConfig(BrainConfigOmni)
}

func DefaultLLMConfigPath() string {
	return configFilePath(defaultLLMConfigFile)
}

// Config returns a copy of the requested config class.
func (c *BrainConfigs) Config(kind BrainConfigKind) (*ModelConfig, error) {
	if c == nil {
		return nil, fmt.Errorf("brain configs is nil")
	}

	base, ok := brainConfigDefaults[kind]
	if !ok {
		return nil, fmt.Errorf("unknown brain config kind %q", kind)
	}

	out := mergeModelConfig(base, c.Defaults)
	if profile := strings.TrimSpace(c.Defaults.Profile); profile != "" {
		profileCfg, ok := c.Profiles[profile]
		if !ok {
			return nil, fmt.Errorf("unknown default profile %q", profile)
		}
		out = mergeModelConfig(out, profileCfg)
	}
	if c.Brains != nil {
		brainCfg := c.Brains[kind]
		if profile := strings.TrimSpace(brainCfg.Profile); profile != "" {
			profileCfg, ok := c.Profiles[profile]
			if !ok {
				return nil, fmt.Errorf("unknown profile %q for brain %q", profile, kind)
			}
			out = mergeModelConfig(out, profileCfg)
		}
		out = mergeModelConfig(out, brainCfg)
	}
	out.Profile = ""
	return cloneBrainConfig(out), nil
}

func (c *BrainConfigs) normalize() {
	if c.Profiles == nil {
		c.Profiles = map[string]ModelConfig{}
	}
	if c.Brains == nil {
		c.Brains = map[BrainConfigKind]ModelConfig{}
	}
}

func mergeModelConfig(base, override ModelConfig) ModelConfig {
	out := base
	if strings.TrimSpace(override.Profile) != "" {
		out.Profile = override.Profile
	}
	if strings.TrimSpace(override.APIKey) != "" {
		out.APIKey = override.APIKey
	}
	if strings.TrimSpace(override.BaseURL) != "" {
		out.BaseURL = override.BaseURL
	}
	if strings.TrimSpace(override.Model) != "" {
		out.Model = override.Model
	}
	if strings.TrimSpace(override.Region) != "" {
		out.Region = override.Region
	}
	if strings.TrimSpace(override.Provider) != "" {
		out.Provider = override.Provider
	}
	if len(override.APIEndpoints) > 0 {
		out.APIEndpoints = cloneEndpoints(override.APIEndpoints)
	}
	return out
}

func cloneBrainConfig(cfg ModelConfig) *ModelConfig {
	out := cfg
	out.APIEndpoints = cloneEndpoints(cfg.APIEndpoints)
	return &out
}

func cloneEndpoints(endpoints map[string]string) map[string]string {
	if endpoints == nil {
		return nil
	}
	out := make(map[string]string, len(endpoints))
	for key, value := range endpoints {
		out[key] = value
	}
	return out
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
