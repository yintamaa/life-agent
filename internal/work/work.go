// Package work is the hub side of agent orchestration: tasks, the nodes that
// run them, approvals, and turning messages into tasks.
package work

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yintamaa/life-agent/internal/agent"
	"github.com/yintamaa/life-agent/internal/model"
	"github.com/yintamaa/life-agent/internal/node"
	"github.com/yintamaa/life-agent/internal/store"
)

// NodeLink is how the hub drives one node (in-process or remote).
type NodeLink interface {
	Name() string
	Info(ctx context.Context) node.Info
	RunTurn(ctx context.Context, req node.TurnReq) error
	Cancel(ctx context.Context, taskID string) error
	Decide(ctx context.Context, approvalID string, allow bool, reason string) error
}

// Preset is a named agent: where it runs, with which CLI, plus standing
// instructions. Defined in config.yaml (no DB, no CRUD UI — keep it small).
type Preset struct {
	Name         string `yaml:"name" json:"name"`   // slug, e.g. "tos-dev"
	Title        string `yaml:"title" json:"title"` // display, e.g. "TOS 开发"
	Node         string `yaml:"node" json:"node"`
	Workspace    string `yaml:"workspace" json:"workspace"`
	Runtime      string `yaml:"runtime" json:"runtime"`
	Model        string `yaml:"model" json:"model,omitempty"`
	Instructions string `yaml:"instructions" json:"instructions,omitempty"`
}

// Notice is something devices should be told about (push notifications).
type Notice struct {
	Kind   string // approval | task_done | task_failed
	TaskID string
	Title  string
	Body   string
	ID     string // approval id for approvals
}

// Manager implements node.Hub and the task API.
type Manager struct {
	store   *store.Store
	Presets []Preset
	// OnNotice, if set, is called (async-safe, must not block) for events a
	// person should hear about even when no page is open.
	OnNotice func(Notice)

	mu    sync.RWMutex
	nodes map[string]NodeLink
	seen  map[string]time.Time
}

// New creates a manager.
func New(st *store.Store) *Manager {
	return &Manager{store: st, nodes: map[string]NodeLink{}, seen: map[string]time.Time{}}
}

// Attach registers a node (replacing an older link with the same name).
func (m *Manager) Attach(l NodeLink) {
	m.mu.Lock()
	m.nodes[l.Name()] = l
	m.seen[l.Name()] = time.Now()
	m.mu.Unlock()
	slog.Info("node attached", "node", l.Name())
}

// Detach removes a node link if it is still the registered one.
func (m *Manager) Detach(l NodeLink) {
	m.mu.Lock()
	if m.nodes[l.Name()] == l {
		delete(m.nodes, l.Name())
	}
	m.seen[l.Name()] = time.Now()
	m.mu.Unlock()
	slog.Info("node detached", "node", l.Name())
}

func (m *Manager) link(name string) (NodeLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.nodes[name]
	if !ok {
		return nil, fmt.Errorf("节点 %s 不在线", name)
	}
	return l, nil
}

// NodeStatus is one node for the UI.
type NodeStatus struct {
	Name     string     `json:"name"`
	Online   bool       `json:"online"`
	LastSeen time.Time  `json:"last_seen"`
	Info     *node.Info `json:"info,omitempty"`
}

// Nodes lists known nodes.
func (m *Manager) Nodes(ctx context.Context) []NodeStatus {
	m.mu.RLock()
	links := make(map[string]NodeLink, len(m.nodes))
	for k, v := range m.nodes {
		links[k] = v
	}
	seen := make(map[string]time.Time, len(m.seen))
	for k, v := range m.seen {
		seen[k] = v
	}
	m.mu.RUnlock()
	var out []NodeStatus
	for name, at := range seen {
		ns := NodeStatus{Name: name, LastSeen: at}
		if l, ok := links[name]; ok {
			info := l.Info(ctx)
			ns.Online, ns.Info = true, &info
		}
		out = append(out, ns)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AgentStatus is a preset plus whether it can take work right now.
type AgentStatus struct {
	Preset
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Agents lists presets with availability (node online, runtime and workspace present).
func (m *Manager) Agents(ctx context.Context) []AgentStatus {
	out := make([]AgentStatus, 0, len(m.Presets))
	for _, p := range m.Presets {
		a := AgentStatus{Preset: p}
		if l, err := m.link(p.Node); err != nil {
			a.Reason = err.Error()
		} else if info := l.Info(ctx); !hasRuntime(info, p.Runtime) {
			a.Reason = "节点上没有可用的 " + p.Runtime
		} else if !hasWorkspace(info, p.Workspace) {
			a.Reason = "节点上没有工作区 " + p.Workspace
		} else {
			a.Available = true
		}
		out = append(out, a)
	}
	return out
}

func (m *Manager) preset(name string) (Preset, bool) {
	for _, p := range m.Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}

func (m *Manager) notice(n Notice) {
	if m.OnNotice != nil {
		m.OnNotice(n)
	}
}

// CreateReq creates a task.
type CreateReq struct {
	Agent           string `json:"agent"` // preset name; fills node/workspace/runtime/model
	Title           string `json:"title"`
	Instruction     string `json:"instruction"`
	ConversationID  string `json:"conversation_id"`
	ContextMessages int    `json:"context_messages"` // default 30 when conversation_id is set
	Node            string `json:"node"`
	Workspace       string `json:"workspace"`
	Runtime         string `json:"runtime"`
	Model           string `json:"model"`
}

func newTaskID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return "t" + time.Now().Format("060102") + "-" + hex.EncodeToString(b[:])
}

func clip(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Create validates, stores and starts a task.
func (m *Manager) Create(ctx context.Context, req CreateReq) (store.Task, error) {
	if strings.TrimSpace(req.Instruction) == "" {
		return store.Task{}, errors.New("instruction required")
	}
	var standing string
	if req.Agent != "" {
		p, ok := m.preset(req.Agent)
		if !ok {
			return store.Task{}, fmt.Errorf("没有名为 %s 的 Agent", req.Agent)
		}
		req.Node, req.Workspace, req.Runtime = p.Node, p.Workspace, p.Runtime
		if req.Model == "" {
			req.Model = p.Model
		}
		standing = p.Instructions
	}
	l, err := m.link(req.Node)
	if err != nil {
		return store.Task{}, err
	}
	info := l.Info(ctx)
	if !hasRuntime(info, req.Runtime) {
		return store.Task{}, fmt.Errorf("节点 %s 上没有可用的 %s", req.Node, req.Runtime)
	}
	if !hasWorkspace(info, req.Workspace) {
		return store.Task{}, fmt.Errorf("节点 %s 上没有工作区 %s", req.Node, req.Workspace)
	}
	prompt, convTitle, err := m.buildPrompt(ctx, req, standing)
	if err != nil {
		return store.Task{}, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = clip(req.Instruction, 40)
		if convTitle != "" {
			title = clip(req.Instruction, 28) + " · " + clip(convTitle, 12)
		}
	}
	t := store.Task{ID: newTaskID(), Title: title, Agent: req.Agent, Instruction: req.Instruction, Status: store.TaskQueued,
		Node: req.Node, Workspace: req.Workspace, Runtime: req.Runtime, Model: req.Model, ConversationID: req.ConversationID}
	if err := m.store.CreateTask(ctx, t); err != nil {
		return t, err
	}
	_, _ = m.store.AppendTaskEvent(ctx, t.ID, "user", map[string]string{"text": req.Instruction}, time.Now())
	if err := l.RunTurn(ctx, node.TurnReq{TaskID: t.ID, Runtime: t.Runtime, Workspace: t.Workspace, Prompt: prompt, Model: t.Model}); err != nil {
		_ = m.store.UpdateTask(ctx, t.ID, store.TaskPatch{Status: ptr(store.TaskFailed), Error: ptr(err.Error())})
		return t, err
	}
	return m.store.GetTask(ctx, t.ID)
}

func ptr[T any](v T) *T { return &v }

func hasRuntime(info node.Info, name string) bool {
	for _, r := range info.Runtimes {
		if r.Name == name && r.Error == "" {
			return true
		}
	}
	return false
}

func hasWorkspace(info node.Info, name string) bool {
	for _, w := range info.Workspaces {
		if w.Name == name {
			return true
		}
	}
	return false
}

var platformName = map[string]string{
	model.PlatformFeishu: "飞书", model.PlatformWeChat: "微信", model.PlatformQQ: "QQ", model.PlatformMail: "邮件",
}

// buildPrompt wraps the instruction with the source conversation's recent
// history — the core of "message-driven work": the agent sees what you saw.
func (m *Manager) buildPrompt(ctx context.Context, req CreateReq, standing string) (string, string, error) {
	var b strings.Builder
	b.WriteString("你是用户在个人工作平台 life-agent 里派出的 Agent。\n")
	if s := strings.TrimSpace(standing); s != "" {
		b.WriteString("\n## 你的设定\n" + s + "\n")
	}
	b.WriteString("\n## 用户的指示\n")
	b.WriteString(strings.TrimSpace(req.Instruction))
	b.WriteString("\n")
	title := ""
	if req.ConversationID != "" {
		conv, err := m.store.GetConversation(ctx, req.ConversationID)
		if err != nil {
			return "", "", fmt.Errorf("来源会话不存在：%w", err)
		}
		title = conv.Title
		n := req.ContextMessages
		if n <= 0 {
			n = 30
		}
		msgs, err := m.store.ListMessages(ctx, conv.ID, time.Time{}, min(n, 200))
		if err != nil {
			return "", "", err
		}
		kind := map[string]string{model.KindP2P: "私聊", model.KindGroup: "群聊", model.KindMail: "邮件线程"}[conv.Kind]
		fmt.Fprintf(&b, "\n## 来源会话\n%s · %s「%s」，最近 %d 条消息（时间 发送者: 内容）：\n\n", platformName[conv.Platform], kind, conv.Title, len(msgs))
		for _, msg := range msgs {
			who := msg.SenderName
			if msg.IsSelf {
				who = "我（用户本人）"
			}
			text := msg.Text
			if msg.Subject != "" {
				text = "[" + msg.Subject + "] " + text
			}
			fmt.Fprintf(&b, "[%s] %s: %s\n", msg.SentAt.Format("01-02 15:04"), who, clip(text, 1500))
		}
	}
	b.WriteString(`
## 输出要求
- 完成后用简洁的中文总结：做了什么、结论是什么、产物在哪（文件路径、分支、链接）。
- 不要自己发送任何 IM 消息或邮件。`)
	if req.ConversationID != "" {
		b.WriteString("\n- 如果需要回复来源会话，在最后给出建议回复，放在 ```reply 代码块里，只放要发出去的正文；用户确认后才会发送。")
	}
	return b.String(), title, nil
}

// Continue runs a follow-up turn in the same agent session.
func (m *Manager) Continue(ctx context.Context, taskID, text string) error {
	t, err := m.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	switch t.Status {
	case store.TaskRunning, store.TaskWaiting, store.TaskQueued:
		return errors.New("任务正在运行，等这一轮结束再继续")
	}
	l, err := m.link(t.Node)
	if err != nil {
		return err
	}
	_, _ = m.store.AppendTaskEvent(ctx, t.ID, "user", map[string]string{"text": text}, time.Now())
	// Mark queued *before* starting: the node reports "running" synchronously.
	if err := m.store.UpdateTask(ctx, t.ID, store.TaskPatch{Status: ptr(store.TaskQueued), Error: ptr("")}); err != nil {
		return err
	}
	if err := l.RunTurn(ctx, node.TurnReq{TaskID: t.ID, Runtime: t.Runtime, Workspace: t.Workspace, Prompt: text, Model: t.Model, SessionID: t.SessionID}); err != nil {
		_ = m.store.UpdateTask(ctx, t.ID, store.TaskPatch{Status: ptr(t.Status)})
		return err
	}
	return nil
}

// Cancel stops the running turn.
func (m *Manager) Cancel(ctx context.Context, taskID string) error {
	t, err := m.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	l, err := m.link(t.Node)
	if err != nil {
		return err
	}
	return l.Cancel(ctx, taskID)
}

// Close marks a task done (user is finished with it).
func (m *Manager) Close(ctx context.Context, taskID string) error {
	t, err := m.store.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if t.Status == store.TaskRunning || t.Status == store.TaskWaiting {
		return errors.New("任务还在运行，先取消")
	}
	return m.store.UpdateTask(ctx, taskID, store.TaskPatch{Status: ptr(store.TaskDone)})
}

// Decide answers an approval. First decision across devices wins; a later
// one gets store.ErrAlreadyDecided with the effective decision.
func (m *Manager) Decide(ctx context.Context, approvalID string, allow bool, by string) (store.Approval, error) {
	a, err := m.store.DecideApproval(ctx, approvalID, allow, by)
	if err != nil {
		return a, err
	}
	t, err := m.store.GetTask(ctx, a.TaskID)
	if err != nil {
		return a, err
	}
	l, lerr := m.link(t.Node)
	if lerr == nil {
		lerr = l.Decide(ctx, approvalID, allow, "")
	}
	if lerr != nil {
		_ = m.store.ExpireApproval(ctx, approvalID)
		a.Status = "expired"
		return a, fmt.Errorf("节点已不再等待这个审批：%w", lerr)
	}
	verdict := map[bool]string{true: "allowed", false: "denied"}[allow]
	_, _ = m.store.AppendTaskEvent(ctx, a.TaskID, "approval_decided", map[string]string{"id": approvalID, "status": verdict, "by": by}, time.Now())
	if t.Status == store.TaskWaiting {
		_ = m.store.UpdateTask(ctx, t.ID, store.TaskPatch{Status: ptr(store.TaskRunning)})
	}
	return a, nil
}

// ---- node.Hub ----

// SessionEvent persists a node event and folds it into task state.
func (m *Manager) SessionEvent(ctx context.Context, taskID string, ev agent.Event) error {
	if _, err := m.store.AppendTaskEvent(ctx, taskID, ev.Type, ev, ev.At); err != nil {
		return err
	}
	var p store.TaskPatch
	switch ev.Type {
	case agent.EvSession:
		p.SessionID = ptr(ev.SessionID)
	case node.EvState:
		switch ev.Text {
		case node.StateRunning:
			p.Status = ptr(store.TaskRunning)
			if ev.Input != "" {
				p.Workdir = ptr(ev.Input)
			}
		case node.StateIdle:
			p.Status = ptr(store.TaskIdle)
			m.noticeTask(ctx, taskID, "task_done", "")
		case node.StateFailed:
			p.Status, p.Error = ptr(store.TaskFailed), ptr(ev.Input)
			m.noticeTask(ctx, taskID, "task_failed", ev.Input)
		case node.StateCancelled:
			p.Status = ptr(store.TaskCanceled)
		}
		if ev.Text != node.StateRunning {
			// The turn is over: nothing waits on its approvals any more.
			if err := m.store.ExpireTaskApprovals(ctx, taskID); err != nil {
				return err
			}
		}
	case agent.EvResult:
		p.Result = ptr(ev.Text)
		if ev.CostUSD > 0 {
			p.CostUSD = ptr(ev.CostUSD)
		}
		if ev.IsError {
			p.Error = ptr(clip(ev.Text, 500))
		}
	default:
		return nil
	}
	return m.store.UpdateTask(ctx, taskID, p)
}

// ApprovalRequest records a pending approval and flags the task.
func (m *Manager) ApprovalRequest(ctx context.Context, req node.ApprovalReq) error {
	if err := m.store.CreateApproval(ctx, store.Approval{ID: req.ID, TaskID: req.TaskID, Tool: req.Tool, Input: req.Input, Reason: req.Reason, ExpiresAt: req.ExpiresAt}); err != nil {
		return err
	}
	_, _ = m.store.AppendTaskEvent(ctx, req.TaskID, agent.EvApproval, req, time.Now())
	title := req.TaskID
	if t, err := m.store.GetTask(ctx, req.TaskID); err == nil {
		title = t.Title
	}
	m.notice(Notice{Kind: "approval", TaskID: req.TaskID, ID: req.ID, Title: title, Body: req.Tool + "：" + clip(req.Input, 120)})
	return m.store.UpdateTask(ctx, req.TaskID, store.TaskPatch{Status: ptr(store.TaskWaiting)})
}

func (m *Manager) noticeTask(ctx context.Context, taskID, kind, detail string) {
	t, err := m.store.GetTask(ctx, taskID)
	if err != nil {
		return
	}
	body := clip(t.Result, 120)
	if kind == "task_failed" {
		body = clip(detail, 120)
	}
	m.notice(Notice{Kind: kind, TaskID: taskID, Title: t.Title, Body: body})
}

// LocalLink adapts an in-process node to NodeLink.
type LocalLink struct{ N *node.Node }

func (l LocalLink) Name() string                                      { return l.N.Name() }
func (l LocalLink) Info(ctx context.Context) node.Info                { return l.N.Info(ctx) }
func (l LocalLink) RunTurn(ctx context.Context, r node.TurnReq) error { return l.N.RunTurn(ctx, r) }
func (l LocalLink) Cancel(_ context.Context, id string) error         { return l.N.Cancel(id) }
func (l LocalLink) Decide(_ context.Context, id string, allow bool, reason string) error {
	return l.N.Decide(id, allow, reason)
}
