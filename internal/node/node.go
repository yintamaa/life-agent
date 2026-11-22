// Package node is the per-machine executor. Each machine that should run
// agents (and, later, host connectors) runs one node; the hub decides what
// to run, the node runs it where the code and credentials live.
//
// The node is deliberately stateless about tasks: every turn request carries
// runtime, workspace and native session id, and the work directory is
// derived from the task id. A node restart loses only in-flight turns.
package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yintamaa/life-agent/internal/agent"
)

// EvState is a node-level event carrying the session state in Text.
const EvState = "state"

// Session states.
const (
	StateRunning   = "running"
	StateIdle      = "idle" // turn finished, can take follow-up input
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// Config for one node.
type Config struct {
	Name       string        `yaml:"name"`
	DataDir    string        `yaml:"data_dir"`
	Claude     RuntimeConfig `yaml:"claude_code"`
	Codex      RuntimeConfig `yaml:"codex"`
	Workspaces []Workspace   `yaml:"workspaces"`
	Approval   Policy        `yaml:"approval"`
}

// RuntimeConfig enables one agent CLI.
type RuntimeConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
	Model   string `yaml:"model"`
}

// Workspace is a directory agents may work in.
type Workspace struct {
	Name     string `yaml:"name"`
	Path     string `yaml:"path"`
	Worktree bool   `yaml:"worktree"` // each task gets its own git worktree + branch
}

// Info is what the node advertises to the hub.
type Info struct {
	Name       string          `json:"name"`
	Runtimes   []RuntimeInfo   `json:"runtimes"`
	Workspaces []WorkspaceInfo `json:"workspaces"`
}

// RuntimeInfo describes one detected CLI.
type RuntimeInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Error   string `json:"error,omitempty"`
}

// WorkspaceInfo describes one workspace.
type WorkspaceInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Worktree bool   `json:"worktree"`
}

// TurnReq asks the node to run one turn of a task.
type TurnReq struct {
	TaskID    string `json:"task_id"`
	Runtime   string `json:"runtime"`
	Workspace string `json:"workspace"`
	Prompt    string `json:"prompt"`
	Model     string `json:"model,omitempty"`
	SessionID string `json:"session_id,omitempty"` // resume this native session
}

// ApprovalReq is sent to the hub when a tool call needs a human.
type ApprovalReq struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Tool      string    `json:"tool"`
	Input     string    `json:"input"`
	Reason    string    `json:"reason"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Hub is how a node reports back (in-process or over the node link).
type Hub interface {
	SessionEvent(ctx context.Context, taskID string, ev agent.Event) error
	ApprovalRequest(ctx context.Context, req ApprovalReq) error
}

// ErrUnknownApproval: the approval already timed out or was decided.
var ErrUnknownApproval = errors.New("审批已过期或已处理")

// Node runs agent turns on this machine.
type Node struct {
	cfg      Config
	hub      Hub
	runtimes map[string]agent.Runtime
	hookCmd  string
	sock     string

	mu       sync.Mutex
	sessions map[string]*session
	pending  map[string]chan hookResponse

	infoMu sync.Mutex
	info   *Info
}

type session struct {
	workdir string
	cancel  context.CancelFunc
}

// New creates a node. selfExe is this binary (used as the hook command).
func New(cfg Config, hub Hub, selfExe string) (*Node, error) {
	if cfg.Name == "" {
		h, _ := os.Hostname()
		cfg.Name = strings.Split(h, ".")[0]
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	cfg.DataDir = expandHome(cfg.DataDir)
	cfg.Approval = cfg.Approval.withDefaults()
	cfg.Claude.Path, cfg.Codex.Path = expandHome(cfg.Claude.Path), expandHome(cfg.Codex.Path)
	n := &Node{cfg: cfg, hub: hub, runtimes: map[string]agent.Runtime{},
		sessions: map[string]*session{}, pending: map[string]chan hookResponse{}}
	if cfg.Claude.Enabled {
		n.runtimes["claude-code"] = &agent.ClaudeCode{Path: cfg.Claude.Path, HookTimeout: cfg.Approval.Timeout}
	}
	if cfg.Codex.Enabled {
		n.runtimes["codex"] = &agent.Codex{Path: cfg.Codex.Path}
	}
	for i, ws := range cfg.Workspaces {
		if ws.Name == "" || ws.Path == "" {
			return nil, fmt.Errorf("workspace #%d needs name and path", i+1)
		}
		cfg.Workspaces[i].Path = expandHome(ws.Path)
	}
	n.hookCmd = shellQuote(selfExe) + " hook"
	// Short path: macOS limits unix socket paths to ~104 bytes.
	n.sock = filepath.Join("/tmp", fmt.Sprintf("lifeagent-%s-%d.sock", safeName(cfg.Name), os.Getpid()))
	return n, nil
}

// RegisterRuntime adds or replaces a runtime (tests, custom CLIs).
func (n *Node) RegisterRuntime(rt agent.Runtime) {
	n.runtimes[rt.Name()] = rt
	n.infoMu.Lock()
	n.info = nil
	n.infoMu.Unlock()
}

// Name returns the node name.
func (n *Node) Name() string { return n.cfg.Name }

// SetHub sets the hub link (used by the remote client, which is created after the node).
func (n *Node) SetHub(h Hub) { n.hub = h }

// ExpandHome expands a leading ~/ to the user's home directory.
func ExpandHome(p string) string { return expandHome(p) }

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

var unsafeRe = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func safeName(s string) string { return unsafeRe.ReplaceAllString(s, "_") }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Info detects runtimes (cached) and lists workspaces.
func (n *Node) Info(ctx context.Context) Info {
	n.infoMu.Lock()
	defer n.infoMu.Unlock()
	if n.info != nil {
		return *n.info
	}
	info := Info{Name: n.cfg.Name}
	names := make([]string, 0, len(n.runtimes))
	for name := range n.runtimes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rt := n.runtimes[name]
		ri := RuntimeInfo{Name: name}
		if v, err := rt.Detect(ctx); err != nil {
			ri.Error = err.Error()
		} else {
			ri.Version = v
		}
		info.Runtimes = append(info.Runtimes, ri)
	}
	for _, ws := range n.cfg.Workspaces {
		info.Workspaces = append(info.Workspaces, WorkspaceInfo{Name: ws.Name, Path: ws.Path, Worktree: ws.Worktree})
	}
	n.info = &info
	return info
}

// Serve runs the local hook server until ctx is done.
func (n *Node) Serve(ctx context.Context) error {
	_ = os.Remove(n.sock)
	ln, err := net.Listen("unix", n.sock)
	if err != nil {
		return fmt.Errorf("hook socket: %w", err)
	}
	_ = os.Chmod(n.sock, 0o600)
	srv := &http.Server{Handler: http.HandlerFunc(n.handleHook), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		_ = os.Remove(n.sock)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (n *Node) workspace(name string) (Workspace, bool) {
	for _, ws := range n.cfg.Workspaces {
		if ws.Name == name {
			return ws, true
		}
	}
	return Workspace{}, false
}

var taskIDRe = regexp.MustCompile(`^[a-zA-Z0-9-]{4,64}$`)

// prepareWorkdir returns the task's working directory, creating a git
// worktree on branch life-agent/<task> when the workspace asks for it.
func (n *Node) prepareWorkdir(ctx context.Context, ws Workspace, taskID string) (string, error) {
	if !ws.Worktree {
		return ws.Path, nil
	}
	dir, err := filepath.Abs(filepath.Join(n.cfg.DataDir, "worktrees", safeName(ws.Name), taskID))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err == nil {
		return dir, nil // follow-up turn
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", ws.Path, "worktree", "add", "-b", "life-agent/"+taskID, dir).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("创建 worktree 失败：%v：%s", err, strings.TrimSpace(string(out)))
	}
	return dir, nil
}

// RunTurn starts one turn asynchronously. Events (including EvState
// running → idle/failed/cancelled) are reported through the hub.
func (n *Node) RunTurn(ctx context.Context, req TurnReq) error {
	if !taskIDRe.MatchString(req.TaskID) {
		return fmt.Errorf("bad task id %q", req.TaskID)
	}
	rt, ok := n.runtimes[req.Runtime]
	if !ok {
		return fmt.Errorf("节点 %s 未启用运行时 %s", n.cfg.Name, req.Runtime)
	}
	ws, ok := n.workspace(req.Workspace)
	if !ok {
		return fmt.Errorf("节点 %s 没有工作区 %s", n.cfg.Name, req.Workspace)
	}
	n.mu.Lock()
	if _, busy := n.sessions[req.TaskID]; busy {
		n.mu.Unlock()
		return errors.New("任务正在运行，等这一轮结束再继续")
	}
	n.sessions[req.TaskID] = &session{} // reserve
	n.mu.Unlock()

	release := func() {
		n.mu.Lock()
		delete(n.sessions, req.TaskID)
		n.mu.Unlock()
	}
	workdir, err := n.prepareWorkdir(ctx, ws, req.TaskID)
	if err != nil {
		release()
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	n.mu.Lock()
	n.sessions[req.TaskID] = &session{workdir: workdir, cancel: cancel}
	n.mu.Unlock()

	model := req.Model
	if model == "" {
		if req.Runtime == "codex" {
			model = n.cfg.Codex.Model
		} else {
			model = n.cfg.Claude.Model
		}
	}
	spec := agent.Spec{
		TaskID: req.TaskID, Workdir: workdir, Prompt: req.Prompt, SessionID: req.SessionID, Model: model,
		Env: []string{EnvHookSock + "=" + n.sock, EnvTaskID + "=" + req.TaskID,
			EnvHookTimeout + "=" + n.cfg.Approval.Timeout.String()},
		HookCommand: n.hookCmd,
	}
	emit := func(ev agent.Event) {
		if ev.At.IsZero() {
			ev.At = time.Now()
		}
		if err := n.hub.SessionEvent(context.Background(), req.TaskID, ev); err != nil {
			slog.Warn("report session event", "task", req.TaskID, "err", err)
		}
	}
	emit(agent.Event{Type: EvState, Text: StateRunning, Input: workdir})
	go func() {
		defer cancel()
		defer release()
		err := rt.Run(runCtx, spec, emit)
		switch {
		case errors.Is(err, context.Canceled):
			emit(agent.Event{Type: EvState, Text: StateCancelled})
		case err != nil:
			emit(agent.Event{Type: EvState, Text: StateFailed, IsError: true, Input: err.Error()})
		default:
			emit(agent.Event{Type: EvState, Text: StateIdle})
		}
	}()
	return nil
}

// Cancel stops a running turn.
func (n *Node) Cancel(taskID string) error {
	n.mu.Lock()
	s, ok := n.sessions[taskID]
	n.mu.Unlock()
	if !ok || s.cancel == nil {
		return errors.New("任务当前没有在运行")
	}
	s.cancel()
	return nil
}

// Decide answers a pending approval.
func (n *Node) Decide(id string, allow bool, reason string) error {
	n.mu.Lock()
	ch, ok := n.pending[id]
	delete(n.pending, id)
	n.mu.Unlock()
	if !ok {
		return ErrUnknownApproval
	}
	d := Deny
	if allow {
		d = Allow
	}
	ch <- hookResponse{Decision: d, Reason: reason}
	return nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (n *Node) handleHook(w http.ResponseWriter, r *http.Request) {
	reply := func(d, reason string) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hookResponse{Decision: d, Reason: reason})
	}
	var req hookRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		reply(Deny, "无法解析请求")
		return
	}
	n.mu.Lock()
	s, ok := n.sessions[req.TaskID]
	n.mu.Unlock()
	if !ok {
		reply(Deny, "任务不在运行中")
		return
	}
	decision, reason := n.cfg.Approval.Decide(req.ToolName, req.ToolInput, s.workdir)
	if decision != Ask {
		reply(decision, reason)
		return
	}
	id := newID()
	ch := make(chan hookResponse, 1)
	n.mu.Lock()
	n.pending[id] = ch
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		delete(n.pending, id)
		n.mu.Unlock()
	}()
	// Answer a bit before the hook's own deadline so it never times out open.
	wait := n.cfg.Approval.Timeout - 5*time.Second
	ar := ApprovalReq{ID: id, TaskID: req.TaskID, Tool: req.ToolName,
		Input: agentInput(req.ToolName, req.ToolInput), Reason: reason, ExpiresAt: time.Now().Add(wait)}
	if err := n.hub.ApprovalRequest(r.Context(), ar); err != nil {
		reply(Deny, "无法提交审批："+err.Error())
		return
	}
	select {
	case resp := <-ch:
		if resp.Reason == "" {
			resp.Reason = map[string]string{Allow: "已在 life-agent 批准", Deny: "已在 life-agent 拒绝"}[resp.Decision]
		}
		reply(resp.Decision, resp.Reason)
	case <-time.After(wait):
		reply(Deny, "等待审批超时，已拒绝")
	case <-r.Context().Done():
	}
}

func agentInput(tool string, in map[string]any) string {
	switch tool {
	case "Bash":
		if s, ok := in["command"].(string); ok {
			return s
		}
	case "Edit", "Write", "MultiEdit":
		if s, ok := in["file_path"].(string); ok {
			return s
		}
	}
	b, _ := json.Marshal(in)
	if len(b) > 2000 {
		b = append(b[:2000], "…"...)
	}
	return string(b)
}
