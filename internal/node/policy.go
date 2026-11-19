package node

import (
	"path/filepath"
	"strings"
	"time"
)

// Policy decides which tool calls run without asking. Default stance
// (the user's choice): execute, but every side-effecting step needs approval.
type Policy struct {
	// AutoAllowTools run without asking (read-only / local tools).
	AutoAllowTools []string `yaml:"auto_allow_tools"`
	// AutoAllowBash: command prefixes allowed without asking, only for simple
	// commands (no ; && || | > < ` $( ), so "git status; rm -rf ~" never matches.
	AutoAllowBash []string `yaml:"auto_allow_bash"`
	// DenyBash: substrings that are always refused.
	DenyBash []string `yaml:"deny_bash"`
	// Timeout: how long to wait for a human before denying.
	Timeout time.Duration `yaml:"timeout"`
}

// DefaultPolicy is used when the config leaves fields empty.
var DefaultPolicy = Policy{
	AutoAllowTools: []string{"Read", "Glob", "Grep", "LS", "TodoWrite", "NotebookRead", "WebSearch", "Skill", "ToolSearch"},
	AutoAllowBash: []string{"git status", "git diff", "git log", "git show", "git branch", "ls", "pwd", "cat ", "head ", "tail ", "wc ",
		"rg ", "grep ", "find ", "go build", "go test", "go vet", "go list", "pnpm test", "pnpm build", "npm test"},
	DenyBash: []string{"rm -rf /", "rm -rf ~", "git push --force", "git push -f", "mkfs", ":(){", "sudo "},
	Timeout:  30 * time.Minute,
}

func (p Policy) withDefaults() Policy {
	if p.AutoAllowTools == nil {
		p.AutoAllowTools = DefaultPolicy.AutoAllowTools
	}
	if p.AutoAllowBash == nil {
		p.AutoAllowBash = DefaultPolicy.AutoAllowBash
	}
	if p.DenyBash == nil {
		p.DenyBash = DefaultPolicy.DenyBash
	}
	if p.Timeout == 0 {
		p.Timeout = DefaultPolicy.Timeout
	}
	return p
}

// Decision values.
const (
	Allow = "allow"
	Deny  = "deny"
	Ask   = "ask"
)

const shellMeta = ";&|<>`$\n"

// Decide classifies one tool call. workdir is the task's working tree.
func (p Policy) Decide(tool string, input map[string]any, workdir string) (string, string) {
	str := func(k string) string { s, _ := input[k].(string); return s }
	for _, t := range p.AutoAllowTools {
		if t == tool {
			return Allow, "只读工具"
		}
	}
	switch tool {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		path := str("file_path")
		if path == "" {
			path = str("notebook_path")
		}
		if inside(workdir, path) {
			return Allow, "仅修改任务工作区内的文件"
		}
		return Ask, "修改工作区之外的文件"
	case "Bash":
		cmd := strings.TrimSpace(str("command"))
		for _, d := range p.DenyBash {
			if d != "" && strings.Contains(cmd, d) {
				return Deny, "命中禁止规则：" + d
			}
		}
		if !strings.ContainsAny(cmd, shellMeta) {
			for _, pre := range p.AutoAllowBash {
				if pre != "" && (cmd == strings.TrimSpace(pre) || strings.HasPrefix(cmd, pre)) {
					return Allow, "白名单命令"
				}
			}
		}
		return Ask, "执行命令"
	}
	return Ask, "调用 " + tool
}

func inside(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
