package work

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yintamaa/life-agent/internal/agent"
	"github.com/yintamaa/life-agent/internal/node"
	"github.com/yintamaa/life-agent/internal/store"
	"github.com/yintamaa/life-agent/internal/triage"
)

func waitOnline(t *testing.T, m *Manager, name string, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range m.Nodes(context.Background()) {
			if n.Name == name && n.Online == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("node %s online=%v not reached", name, want)
}

func TestRemoteNodeLink(t *testing.T) {
	ctx := context.Background()
	tri, _ := triage.New(triage.Config{})
	st, err := store.Open(filepath.Join(t.TempDir(), "hub.db"), tri)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := New(st)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/node", m.ServeNode)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	nodeDir := t.TempDir()
	newNode := func() (*node.Node, *node.Client, context.CancelFunc) {
		n, err := node.New(node.Config{Name: "laptop", DataDir: nodeDir, Workspaces: []node.Workspace{{Name: "repo", Path: t.TempDir()}},
			Approval: node.Policy{Timeout: 10 * time.Second}}, nil, "/bin/true")
		if err != nil {
			t.Fatal(err)
		}
		n.RegisterRuntime(&fakeRT{})
		cl, err := node.NewClient(ts.URL, "", n, filepath.Join(nodeDir, "link"))
		if err != nil {
			t.Fatal(err)
		}
		nctx, cancel := context.WithCancel(ctx)
		go n.Serve(nctx)
		go cl.Run(nctx)
		return n, cl, func() { cancel(); cl.Close() }
	}
	_, _, stop := newNode()
	waitOnline(t, m, "laptop", true)

	// Full flow over the WebSocket: create → approval → decide → idle.
	task, err := m.Create(ctx, CreateReq{Instruction: "do it", Node: "laptop", Workspace: "repo", Runtime: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, task.ID, store.TaskWaiting)
	pend, _ := st.ListApprovals(ctx, task.ID, true)
	if len(pend) != 1 {
		t.Fatalf("pending %+v", pend)
	}
	if _, err := m.Decide(ctx, pend[0].ID, true, "phone"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, task.ID, store.TaskIdle)

	// Disconnect, report an event while offline (durable outbox), reconnect.
	stop()
	waitOnline(t, m, "laptop", false)
	if _, err := m.Create(ctx, CreateReq{Instruction: "x", Node: "laptop", Workspace: "repo", Runtime: "claude-code"}); err == nil {
		t.Fatal("offline node must be rejected")
	}
	_, cl2, stop2 := newNode()
	defer stop2()
	_ = cl2.SessionEvent(ctx, task.ID, agent.Event{Type: agent.EvLog, Text: "offline-note", At: time.Now()})
	waitOnline(t, m, "laptop", true)
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs, _ := st.TaskEvents(ctx, task.ID, 0, 0)
		count := 0
		for _, e := range evs {
			if e.Type == "log" && strings.Contains(string(e.Payload), "offline-note") {
				count++
			}
		}
		if count == 1 {
			break
		}
		if count > 1 || time.Now().After(deadline) {
			t.Fatalf("offline-note delivered %d times", count)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
