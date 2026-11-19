package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"crypto/rand"
)

// ClaudeCode drives `claude -p --output-format stream-json`.
//
// Approvals: we pass a per-invocation settings file that registers a
// PreToolUse hook (`lifeagent hook`). Every tool call blocks on that hook,
// which asks the node's policy and, if needed, a human on any device.
// `--permission-prompts none` makes sure nothing else can prompt (there is
// no TTY), so the hook's allow/deny is the decision.
type ClaudeCode struct {
	Path           string
	PermissionMode string        // default acceptEdits
	HookTimeout    time.Duration // how long a tool call may wait for a human
}

func (c *ClaudeCode) Name() string { return "claude-code" }

func (c *ClaudeCode) path() string {
	if c.Path == "" {
		return "claude"
	}
	return c.Path
}

func (c *ClaudeCode) Detect(ctx context.Context) (string, error) { return version(ctx, c.path()) }

// NewSessionID returns a random UUIDv4 (Claude's --session-id requires one).
func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// hookSettings is the --settings payload registering our PreToolUse hook.
func (c *ClaudeCode) hookSettings(command string) ([]byte, error) {
	timeout := c.HookTimeout
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	return json.Marshal(map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": "*",
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": command,
					"timeout": int(timeout.Seconds()) + 60,
				}},
			}},
		},
	})
}

// Run implements Runtime. spec.SessionID: empty → a new session is created
// with a fresh id (reported via EvSession); otherwise the turn resumes it.
func (c *ClaudeCode) Run(ctx context.Context, spec Spec, emit func(Event)) error {
	mode := c.PermissionMode
	if mode == "" {
		mode = "acceptEdits"
	}
	args := []string{"-p", spec.Prompt, "--output-format", "stream-json", "--verbose",
		"--permission-mode", mode, "--permission-prompts", "none"}
	if spec.SessionID == "" {
		id := NewSessionID()
		args = append(args, "--session-id", id)
		emit(Event{Type: EvSession, SessionID: id, At: time.Now()})
	} else {
		args = append(args, "--resume", spec.SessionID)
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.HookCommand != "" {
		settings, err := c.hookSettings(spec.HookCommand)
		if err != nil {
			return err
		}
		f, err := os.CreateTemp("", "lifeagent-claude-*.json")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err := f.Write(settings); err != nil {
			f.Close()
			return err
		}
		f.Close()
		args = append(args, "--settings", f.Name())
	}
	cmd := exec.CommandContext(ctx, c.path(), args...)
	cmd.Dir = spec.Workdir
	cmd.Env = append(os.Environ(), spec.Env...)
	var result *Event
	err := runJSONL(ctx, cmd, func(line []byte) {
		for _, ev := range parseClaude(line) {
			ev.At = time.Now()
			if ev.Type == EvResult {
				e := ev
				result = &e
				continue
			}
			emit(ev)
		}
	})
	if result == nil {
		result = &Event{Type: EvResult, At: time.Now()}
	}
	if err != nil {
		result.IsError = true
		if result.Text == "" {
			result.Text = err.Error()
		}
	}
	emit(*result)
	return err
}

// parseClaude maps one stream-json line to zero or more Events.
func parseClaude(line []byte) []Event {
	var v struct {
		Type      string  `json:"type"`
		Subtype   string  `json:"subtype"`
		SessionID string  `json:"session_id"`
		Result    string  `json:"result"`
		IsError   bool    `json:"is_error"`
		CostUSD   float64 `json:"total_cost_usd"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &v) != nil {
		return nil
	}
	switch v.Type {
	case "system":
		if v.Subtype == "init" && v.SessionID != "" {
			return []Event{{Type: EvSession, SessionID: v.SessionID}}
		}
	case "result":
		return []Event{{Type: EvResult, Text: v.Result, IsError: v.IsError, CostUSD: v.CostUSD, SessionID: v.SessionID}}
	case "assistant", "user":
		var blocks []map[string]any
		if json.Unmarshal(v.Message.Content, &blocks) != nil {
			return nil // plain string content (user prompt echo)
		}
		var out []Event
		for _, b := range blocks {
			str := func(k string) string { s, _ := b[k].(string); return s }
			switch str("type") {
			case "text":
				if t := str("text"); t != "" {
					out = append(out, Event{Type: EvText, Text: t})
				}
			case "thinking":
				if t := str("thinking"); t != "" {
					out = append(out, Event{Type: EvThinking, Text: clip(t, 2000)})
				}
			case "tool_use":
				out = append(out, Event{Type: EvTool, Tool: str("name"), Input: toolInput(str("name"), b["input"])})
			case "tool_result":
				out = append(out, Event{Type: EvToolResult, Text: clip(toolResultText(b["content"]), 2000), IsError: b["is_error"] == true})
			}
		}
		return out
	}
	return nil
}

// toolInput renders the most useful part of a tool call for humans.
func toolInput(tool string, in any) string {
	m, _ := in.(map[string]any)
	pick := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	var s string
	switch tool {
	case "Bash":
		s = pick("command")
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		s = pick("file_path", "notebook_path")
	case "Grep", "Glob":
		s = pick("pattern")
	case "WebFetch":
		s = pick("url")
	case "WebSearch":
		s = pick("query")
	case "Task", "Agent":
		s = pick("description", "prompt")
	}
	if s == "" {
		return compactJSON(in, 500)
	}
	if tool == "Read" || tool == "Write" || tool == "Edit" {
		s = filepath.Clean(s)
	}
	return clip(s, 500)
}

func toolResultText(c any) string {
	switch t := c.(type) {
	case string:
		return t
	case []any:
		var s string
		for _, x := range t {
			if m, ok := x.(map[string]any); ok {
				if txt, ok := m["text"].(string); ok {
					s += txt
				}
			}
		}
		return s
	}
	return ""
}
