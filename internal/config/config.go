// Package config loads config.yaml.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/yintamaa/life-agent/internal/api"
	"github.com/yintamaa/life-agent/internal/connector/feishu"
	"github.com/yintamaa/life-agent/internal/connector/mail"
	"github.com/yintamaa/life-agent/internal/connector/qq"
	"github.com/yintamaa/life-agent/internal/connector/wechat"
	"github.com/yintamaa/life-agent/internal/triage"
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
	Mail    []mail.Account `yaml:"mail"`
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
