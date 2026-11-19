// Package agent drives existing coding-agent CLIs (Claude Code, Codex)
// headlessly. We never implement an agent runtime ourselves: each adapter
// spawns the CLI the user already installed and logged into, and normalizes
// its event stream into Event.
//
// A Run is one *turn*: start a new session or resume an existing one with a
// follow-up prompt, stream events until the CLI exits.
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Event types.
const (
	EvSession    = "session"     // native session id known (Event.SessionID)
	EvText       = "text"        // assistant text
	EvThinking   = "thinking"    // reasoning summary (may be empty for some models)
	EvTool       = "tool"        // tool call started/completed (Tool, Input)
	EvToolResult = "tool_result" // tool output (truncated)
	EvApproval   = "approval"    // a tool call is waiting for human approval (emitted by the node)
	EvLog        = "log"         // CLI warnings etc.
	EvError      = "error"
	EvResult     = "result" // turn finished (Text = final message, CostUSD, IsError)
)

// Event is one normalized item of an agent's output stream.
type Event struct {
	Type      string    `json:"type"`
	Text      string    `json:"text,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	Input     string    `json:"input,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	CostUSD   float64   `json:"cost_usd,omitempty"`
	IsError   bool      `json:"is_error,omitempty"`
	At        time.Time `json:"at"`
}

// Spec describes one turn.
type Spec struct {
	TaskID    string
	Workdir   string
	Prompt    string
	SessionID string // native session/thread id to resume; empty = new session
	Model     string
	// Env is appended to the CLI's environment (e.g. hook socket for approvals).
	Env []string
	// HookCommand is the command Claude Code runs for PreToolUse approval.
	HookCommand string
}

// Runtime is one agent CLI.
type Runtime interface {
	Name() string
	// Detect returns the CLI version, or an error if it isn't installed.
	Detect(ctx context.Context) (string, error)
	// Run executes one turn and blocks until the CLI exits or ctx is done.
	Run(ctx context.Context, spec Spec, emit func(Event)) error
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func compactJSON(v any, n int) string {
	if s, ok := v.(string); ok {
		return clip(s, n)
	}
	b, _ := json.Marshal(v)
	return clip(string(b), n)
}

// runJSONL starts cmd, feeds every stdout line to onLine, and collects a
// stderr tail for error reporting.
func runJSONL(ctx context.Context, cmd *exec.Cmd, onLine func([]byte)) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 8 << 10}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) > 0 && line[0] == '{' {
			onLine(line)
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("%s 退出：%w：%s", cmd.Args[0], err, clip(stderr.String(), 500))
	}
	return nil
}

type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

func version(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%s 不可用：%w", path, err)
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), nil
}
