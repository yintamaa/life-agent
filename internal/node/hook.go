package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// Environment passed to the agent CLI so the hook can find its node.
const (
	EnvHookSock = "LIFEAGENT_HOOK_SOCK"
	EnvTaskID   = "LIFEAGENT_TASK_ID"
	// EnvHookTimeout lets the hook wait as long as the node's approval policy.
	EnvHookTimeout = "LIFEAGENT_HOOK_TIMEOUT"
)

// hookRequest is what `lifeagent hook` forwards to the node.
type hookRequest struct {
	TaskID    string         `json:"task_id"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	ToolUseID string         `json:"tool_use_id"`
	Cwd       string         `json:"cwd"`
}

type hookResponse struct {
	Decision string `json:"decision"` // allow | deny
	Reason   string `json:"reason"`
}

// RunHook implements the `lifeagent hook` subcommand, invoked by Claude Code
// as a PreToolUse hook. Contract (code.claude.com/docs/en/hooks):
// stdin = hook event JSON; stdout = hookSpecificOutput decision; exit 0.
//
// SAFETY: a PreToolUse hook that crashes, exits non-zero (other than 2) or
// times out does NOT block the tool call. So this function never fails
// open: every error path prints an explicit deny and exits 0, and it owns
// its deadline (well before the hook timeout Claude enforces).
func RunHook(stdin io.Reader, stdout io.Writer, timeout time.Duration) (exitCode int) {
	decide := func(decision, reason string) int {
		out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       decision,
			"permissionDecisionReason": reason,
		}})
		if _, err := stdout.Write(append(out, '\n')); err != nil {
			return 2 // exit 2 = blocking error, the safe failure mode
		}
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			exitCode = decide("deny", fmt.Sprintf("life-agent 审批钩子异常：%v", r))
		}
	}()

	var ev struct {
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
		ToolUseID string         `json:"tool_use_id"`
		Cwd       string         `json:"cwd"`
	}
	if err := json.NewDecoder(io.LimitReader(stdin, 4<<20)).Decode(&ev); err != nil {
		return decide("deny", "life-agent 无法解析钩子输入")
	}
	sock, task := os.Getenv(EnvHookSock), os.Getenv(EnvTaskID)
	if sock == "" || task == "" {
		return decide("deny", "life-agent 节点信息缺失，拒绝执行")
	}
	if d, err := time.ParseDuration(os.Getenv(EnvHookTimeout)); err == nil && d > 0 {
		timeout = d
	}
	if timeout <= 0 {
		timeout = DefaultPolicy.Timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	body, _ := json.Marshal(hookRequest{TaskID: task, ToolName: ev.ToolName, ToolInput: ev.ToolInput, ToolUseID: ev.ToolUseID, Cwd: ev.Cwd})
	cl := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://node/hook", bytes.NewReader(body))
	resp, err := cl.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return decide("deny", "等待审批超时，已拒绝")
		}
		return decide("deny", "life-agent 节点不可达，已拒绝："+err.Error())
	}
	defer resp.Body.Close()
	var hr hookResponse
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&hr) != nil {
		return decide("deny", fmt.Sprintf("life-agent 节点返回异常（%d），已拒绝", resp.StatusCode))
	}
	if hr.Decision != "allow" {
		return decide("deny", hr.Reason)
	}
	return decide("allow", hr.Reason)
}
