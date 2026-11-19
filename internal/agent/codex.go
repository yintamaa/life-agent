package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Codex drives `codex exec --json`.
//
// Codex's non-interactive mode has no channel for a human to answer approval
// prompts (that needs the experimental app-server), so safety comes from the
// sandbox instead: workspace-write (writes only inside the task worktree,
// network off by default) with approval_policy=never.
type Codex struct {
	Path    string
	Sandbox string // default workspace-write
}

func (c *Codex) Name() string { return "codex" }

func (c *Codex) path() string {
	if c.Path == "" {
		return "codex"
	}
	return c.Path
}

func (c *Codex) Detect(ctx context.Context) (string, error) { return version(ctx, c.path()) }

func (c *Codex) args(spec Spec) []string {
	sandbox := c.Sandbox
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	a := []string{"exec", "--json", "--sandbox", sandbox, "--skip-git-repo-check",
		"-c", `approval_policy="never"`, "-C", spec.Workdir}
	if spec.Model != "" {
		a = append(a, "-m", spec.Model)
	}
	if spec.SessionID != "" {
		a = append(a, "resume", spec.SessionID)
	}
	return append(a, spec.Prompt)
}

// Run implements Runtime.
func (c *Codex) Run(ctx context.Context, spec Spec, emit func(Event)) error {
	cmd := exec.CommandContext(ctx, c.path(), c.args(spec)...)
	cmd.Dir = spec.Workdir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Stdin = nil
	var last string
	var usage map[string]any
	err := runJSONL(ctx, cmd, func(line []byte) {
		ev, final, u := parseCodex(line)
		if final != "" {
			last = final
		}
		if u != nil {
			usage = u
		}
		if ev != nil {
			ev.At = time.Now()
			emit(*ev)
		}
	})
	res := Event{Type: EvResult, Text: last, At: time.Now()}
	if usage != nil {
		res.Input = compactJSON(usage, 300)
	}
	if err != nil {
		res.IsError = true
		if res.Text == "" {
			res.Text = err.Error()
		}
	}
	emit(res)
	return err
}

// parseCodex maps one JSONL line to an Event. It also returns the latest
// agent message (final answer candidate) and turn usage when present.
func parseCodex(line []byte) (ev *Event, final string, usage map[string]any) {
	var v struct {
		Type     string         `json:"type"`
		ThreadID string         `json:"thread_id"`
		Usage    map[string]any `json:"usage"`
		Message  string         `json:"message"`
		Error    struct {
			Message string `json:"message"`
		} `json:"error"`
		Item map[string]any `json:"item"`
	}
	if json.Unmarshal(line, &v) != nil {
		return nil, "", nil
	}
	str := func(k string) string { s, _ := v.Item[k].(string); return s }
	switch v.Type {
	case "thread.started":
		return &Event{Type: EvSession, SessionID: v.ThreadID}, "", nil
	case "turn.completed":
		return nil, "", v.Usage
	case "turn.failed":
		return &Event{Type: EvError, Text: v.Error.Message, IsError: true}, "", nil
	case "error":
		return &Event{Type: EvError, Text: v.Message, IsError: true}, "", nil
	case "item.started", "item.completed":
		done := v.Type == "item.completed"
		switch str("type") {
		case "agent_message":
			if done {
				return &Event{Type: EvText, Text: str("text")}, str("text"), nil
			}
		case "reasoning":
			if done && str("text") != "" {
				return &Event{Type: EvThinking, Text: clip(str("text"), 2000)}, "", nil
			}
		case "command_execution":
			if !done {
				return &Event{Type: EvTool, Tool: "shell", Input: clip(str("command"), 500)}, "", nil
			}
			out := str("aggregated_output")
			if code, ok := v.Item["exit_code"].(float64); ok && code != 0 {
				out = fmt.Sprintf("exit %d\n%s", int(code), out)
			}
			return &Event{Type: EvToolResult, Tool: "shell", Text: clip(out, 2000)}, "", nil
		case "file_change":
			if done {
				return &Event{Type: EvTool, Tool: "edit", Input: compactJSON(v.Item["changes"], 500)}, "", nil
			}
		case "mcp_tool_call":
			if !done {
				return &Event{Type: EvTool, Tool: str("server") + "." + str("tool"), Input: compactJSON(v.Item["arguments"], 500)}, "", nil
			}
		case "web_search":
			if !done {
				return &Event{Type: EvTool, Tool: "web_search", Input: clip(str("query"), 300)}, "", nil
			}
		case "error":
			// Codex reports config deprecations etc. as error items; they don't fail the turn.
			return &Event{Type: EvLog, Text: clip(str("message"), 500)}, "", nil
		}
	}
	return nil, "", nil
}
