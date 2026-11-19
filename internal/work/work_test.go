package work

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yintamaa/life-agent/internal/agent"
	"github.com/yintamaa/life-agent/internal/model"
	"github.com/yintamaa/life-agent/internal/node"
	"github.com/yintamaa/life-agent/internal/store"
	"github.com/yintamaa/life-agent/internal/triage"
)

type fakeRT struct {
	mu    sync.Mutex
	specs []agent.Spec
}

func (f *fakeRT) Name() string                           { return "claude-code" }
func (f *fakeRT) Detect(context.Context) (string, error) { return "test", nil }
func (f *fakeRT) Run(ctx context.Context, spec agent.Spec, emit func(agent.Event)) error {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	turn := len(f.specs)
	f.mu.Unlock()
	for _, kv := range spec.Env {
		k, v, _ := strings.Cut(kv, "=")
		os.Setenv(k, v)
	}
	if spec.SessionID == "" {
		emit(agent.Event{Type: agent.EvSession, SessionID: "sess-1"})
	}
	if turn == 1 { // first turn needs approval for a push
		in, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "git push origin feat"}})
		var out bytes.Buffer
		node.RunHook(bytes.NewReader(in), &out, 5*time.Second)
		emit(agent.Event{Type: agent.EvToolResult, Text: out.String()})
	}
	emit(agent.Event{Type: agent.EvResult, Text: "完成\n```reply\n好的，已处理\n```", CostUSD: 0.01})
	return nil
}

func waitStatus(t *testing.T, st *store.Store, id, want string) store.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		tk, err := st.GetTask(context.Background(), id)
		if err == nil && tk.Status == want {
			return tk
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s status %q, want %q", id, tk.Status, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMessageToTaskFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tri, _ := triage.New(triage.Config{})
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), tri)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.UpsertConversation(ctx, model.Conversation{ID: "feishu:oc1", Source: "feishu", Platform: "feishu", NativeID: "oc1", Kind: "group", Title: "存储值班群"})
	_ = st.IngestMessages(ctx, []model.Message{{ID: "feishu:m1", ConversationID: "feishu:oc1", Source: "feishu", SenderName: "王五", Text: "bucket 列表接口 500 了，谁看下", Type: "text", SentAt: time.Now()}})

	m := New(st)
	wsDir := t.TempDir()
	n, err := node.New(node.Config{Name: "mac", DataDir: t.TempDir(), Workspaces: []node.Workspace{{Name: "repo", Path: wsDir}},
		Approval: node.Policy{Timeout: 10 * time.Second}}, m, "/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	rt := &fakeRT{}
	n.RegisterRuntime(rt)
	go n.Serve(ctx)
	time.Sleep(100 * time.Millisecond)
	m.Attach(LocalLink{N: n})

	if _, err := m.Create(ctx, CreateReq{Instruction: "排查一下", Node: "mac", Workspace: "repo", Runtime: "codex"}); err == nil {
		t.Fatal("runtime not on node must be rejected")
	}
	task, err := m.Create(ctx, CreateReq{Instruction: "排查这个 500", ConversationID: "feishu:oc1", Node: "mac", Workspace: "repo", Runtime: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	// The agent sees the conversation.
	rt.mu.Lock()
	prompt := rt.specs[0].Prompt
	rt.mu.Unlock()
	for _, want := range []string{"排查这个 500", "飞书 · 群聊「存储值班群」", "王五: bucket 列表接口 500 了", "```reply"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	waitStatus(t, st, task.ID, store.TaskWaiting)
	pend, _ := st.ListApprovals(ctx, task.ID, true)
	if len(pend) != 1 || pend[0].Input != "git push origin feat" {
		t.Fatalf("pending approvals %+v", pend)
	}
	// Phone and laptop tap at the same time: first wins, second learns the outcome.
	if _, err := m.Decide(ctx, pend[0].ID, true, "phone"); err != nil {
		t.Fatal(err)
	}
	a, err := m.Decide(ctx, pend[0].ID, false, "laptop")
	if err != store.ErrAlreadyDecided || a.Status != "allowed" || a.DecidedBy != "phone" {
		t.Fatalf("second decision: %+v %v", a, err)
	}
	tk := waitStatus(t, st, task.ID, store.TaskIdle)
	if tk.SessionID != "sess-1" || !strings.Contains(tk.Result, "```reply") || tk.CostUSD != 0.01 {
		t.Fatalf("task after turn: %+v", tk)
	}
	evs, _ := st.TaskEvents(ctx, task.ID, 0, 0)
	if len(evs) < 5 || evs[0].Type != "user" {
		t.Fatalf("events: %+v", evs)
	}
	// Incremental fetch continues from the last seq.
	more, _ := st.TaskEvents(ctx, task.ID, evs[len(evs)-1].Seq, 0)
	if len(more) != 0 {
		t.Fatalf("expected no events after last seq, got %d", len(more))
	}

	// Follow-up resumes the same native session.
	if err := m.Continue(ctx, task.ID, "再补一个单测"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, task.ID, store.TaskIdle)
	rt.mu.Lock()
	if len(rt.specs) != 2 || rt.specs[1].SessionID != "sess-1" || rt.specs[1].Prompt != "再补一个单测" {
		t.Fatalf("follow-up spec %+v", rt.specs)
	}
	rt.mu.Unlock()
	if err := m.Close(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	active, pending, _ := st.WorkCounts(ctx)
	if active != 0 || pending != 0 {
		t.Fatalf("counts active=%d pending=%d", active, pending)
	}
}

func TestApprovalsExpireWhenTurnEnds(t *testing.T) {
	ctx := context.Background()
	tri, _ := triage.New(triage.Config{})
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), tri)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := New(st)
	_ = st.CreateTask(ctx, store.Task{ID: "t-0001", Title: "x", Status: store.TaskRunning, Node: "n", Workspace: "w", Runtime: "claude-code"})
	_ = m.ApprovalRequest(ctx, node.ApprovalReq{ID: "a1", TaskID: "t-0001", Tool: "Bash", Input: "git push", ExpiresAt: time.Now().Add(time.Hour)})
	if p, _ := st.ListApprovals(ctx, "t-0001", true); len(p) != 1 {
		t.Fatalf("want 1 pending, got %d", len(p))
	}
	_ = m.SessionEvent(ctx, "t-0001", agent.Event{Type: node.EvState, Text: node.StateCancelled, At: time.Now()})
	if p, _ := st.ListApprovals(ctx, "t-0001", true); len(p) != 0 {
		t.Fatalf("cancelled task must not leave pending approvals: %+v", p)
	}
	if _, pending, _ := st.WorkCounts(ctx); pending != 0 {
		t.Fatalf("badge count %d", pending)
	}
}
