// Package config loads config.yaml.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/yintamaa/life-agent/internal/api"
	"github.com/yintamaa/life-agent/internal/connector/discord"
	"github.com/yintamaa/life-agent/internal/connector/feishu"
	"github.com/yintamaa/life-agent/internal/connector/mail"
	"github.com/yintamaa/life-agent/internal/connector/qq"
	"github.com/yintamaa/life-agent/internal/connector/wechat"
	"github.com/yintamaa/life-agent/internal/node"
	"github.com/yintamaa/life-agent/internal/triage"
	"github.com/yintamaa/life-agent/internal/work"
)

// Config is the whole file.
type Config struct {
	Listen  string         `yaml:"listen"`
	DataDir string         `yaml:"data_dir"`
	Auth    api.AuthConfig `yaml:"auth"`
	Triage  triage.Config  `yaml:"triage"`
	Feishu  feishu.Config  `yaml:"feishu"`
	WeChat  wechat.Config  `yaml:"wechat"`
	QQ      qq.Config      `yaml:"qq"`
	Discord discord.Config `yaml:"discord"`
	Mail    []mail.Account `yaml:"mail"`
	// Node runs agents on the hub machine itself (in-process node).
	Node NodeConfig `yaml:"node"`
	// Agents are named presets (where / which CLI / standing instructions).
	Agents []work.Preset `yaml:"agents"`
	Push   PushConfig    `yaml:"push"`
}

// PushConfig controls Web Push notifications.
type PushConfig struct {
	Disabled bool   `yaml:"disabled"`
	Subject  string `yaml:"subject"` // VAPID contact (mailto: or https URL)
}

// NodeConfig enables the in-process node.
type NodeConfig struct {
	Enabled     bool `yaml:"enabled"`
	node.Config `yaml:",inline"`
}

// Load reads and validates the file at path.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:7788"
	}
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	names := map[string]bool{}
	for _, a := range c.Agents {
		if a.Name == "" || a.Node == "" || a.Workspace == "" || a.Runtime == "" {
			return nil, fmt.Errorf("agent preset needs name, node, workspace and runtime: %q", a.Name)
		}
		if names[a.Name] {
			return nil, fmt.Errorf("duplicate agent preset %q", a.Name)
		}
		names[a.Name] = true
	}
	seen := map[string]bool{}
	for _, a := range c.Mail {
		if a.ID == "" || a.Host == "" || a.Username == "" {
			return nil, fmt.Errorf("mail account needs id, host and username: %+v", a.ID)
		}
		if seen[a.ID] {
			return nil, fmt.Errorf("duplicate mail account id %q", a.ID)
		}
		seen[a.ID] = true
	}
	return &c, nil
}

// NodeFile is the config of a standalone node (`lifeagent node`), e.g. on
// the work laptop, connecting to a remote hub.
type NodeFile struct {
	HubURL      string `yaml:"hub_url"` // e.g. https://mac-mini.<tailnet>.ts.net:8443
	Token       string `yaml:"token"`   // one of the hub's auth.tokens
	node.Config `yaml:",inline"`
}

// LoadNode reads a standalone node config.
func LoadNode(path string) (*NodeFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c NodeFile
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.HubURL == "" {
		return nil, fmt.Errorf("%s: hub_url required", path)
	}
	if c.DataDir == "" {
		c.DataDir = "data-node"
	}
	c.DataDir = node.ExpandHome(c.DataDir)
	return &c, nil
}
