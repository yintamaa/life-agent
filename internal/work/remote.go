package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yintamaa/life-agent/internal/node"
)

var upgrader = websocket.Upgrader{
	// Nodes are not browsers; refuse cross-site browser upgrades outright.
	CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" },
}

// ServeNode accepts a node link (GET /api/node, WebSocket). Authentication
// happens before this handler (bearer token or verified Tailscale identity).
func (m *Manager) ServeNode(w http.ResponseWriter, r *http.Request) {
	raw, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the HTTP error
	}
	conn := &rconn{Conn: raw}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var hello node.Frame
	if err := conn.ReadJSON(&hello); err != nil || hello.Type != "hello" || hello.Node == "" || hello.Info == nil {
		_ = conn.close(4002, "expected hello")
		return
	}
	ctx := context.Background()
	src := "node:" + hello.Node
	cur, err := m.store.Cursor(ctx, src, "acked")
	if err != nil {
		_ = conn.close(1011, "store error")
		return
	}
	acked, _ := strconv.ParseInt(cur, 10, 64)
	if err := conn.send(node.Frame{Type: "welcome", Acked: acked}); err != nil {
		return
	}
	link := &remoteLink{name: hello.Node, info: *hello.Info, conn: conn, pending: map[string]chan node.Frame{}}
	if old := m.replace(link); old != nil {
		_ = old.conn.close(4001, "replaced by a newer connection")
	}
	defer m.Detach(link)
	defer link.failPending()

	// Liveness: ping every 20s; 60s silence = dead.
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	conn.SetPingHandler(func(s string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.mu.Lock()
		defer conn.mu.Unlock()
		return conn.WriteControl(websocket.PongMessage, []byte(s), time.Now().Add(5*time.Second))
	})
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				conn.mu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
				conn.mu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

	for {
		var f node.Frame
		if err := conn.ReadJSON(&f); err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		m.touch(hello.Node)
		switch f.Type {
		case "res":
			link.resolve(f)
		case "event":
			if f.Seq <= acked { // duplicate after reconnect: already persisted
				_ = conn.send(node.Frame{Type: "ack", Seq: f.Seq})
				continue
			}
			if err := m.applyEvent(ctx, f); err != nil {
				// Don't ack; drop the link so the node resends from `acked`.
				slog.Error("node event", "node", hello.Node, "seq", f.Seq, "err", err)
				_ = conn.close(1011, "event failed")
				return
			}
			acked = f.Seq
			if err := m.store.SetCursor(ctx, src, "acked", strconv.FormatInt(acked, 10)); err != nil {
				_ = conn.close(1011, "store error")
				return
			}
			_ = conn.send(node.Frame{Type: "ack", Seq: f.Seq})
		}
	}
}

func (m *Manager) applyEvent(ctx context.Context, f node.Frame) error {
	switch f.Kind {
	case "session_event":
		if f.Event == nil || f.TaskID == "" {
			return errors.New("bad session_event")
		}
		return m.SessionEvent(ctx, f.TaskID, *f.Event)
	case "approval_request":
		if f.Approval == nil {
			return errors.New("bad approval_request")
		}
		return m.ApprovalRequest(ctx, *f.Approval)
	}
	return nil // unknown kinds from newer nodes are ignored (and acked)
}

// replace attaches l and returns the link it displaced, if any.
func (m *Manager) replace(l NodeLink) *remoteLink {
	m.mu.Lock()
	old, _ := m.nodes[l.Name()].(*remoteLink)
	m.nodes[l.Name()] = l
	m.seen[l.Name()] = time.Now()
	m.mu.Unlock()
	slog.Info("node attached", "node", l.Name())
	return old
}

func (m *Manager) touch(name string) {
	m.mu.Lock()
	m.seen[name] = time.Now()
	m.mu.Unlock()
}

type rconn struct {
	*websocket.Conn
	mu sync.Mutex
}

func (c *rconn) send(f node.Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return c.WriteJSON(f)
}

func (c *rconn) close(code int, reason string) error {
	c.mu.Lock()
	_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	c.mu.Unlock()
	return c.Conn.Close()
}

// remoteLink drives a node over its WebSocket.
type remoteLink struct {
	name string
	info node.Info
	conn *rconn
	seq  atomic.Int64

	mu      sync.Mutex
	pending map[string]chan node.Frame
	closed  bool
}

func (l *remoteLink) Name() string                   { return l.name }
func (l *remoteLink) Info(context.Context) node.Info { return l.info }

func (l *remoteLink) call(ctx context.Context, method string, params any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	id := strconv.FormatInt(l.seq.Add(1), 10)
	ch := make(chan node.Frame, 1)
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return fmt.Errorf("节点 %s 已断开", l.name)
	}
	l.pending[id] = ch
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.pending, id)
		l.mu.Unlock()
	}()
	if err := l.conn.send(node.Frame{Type: "req", ID: id, Method: method, Params: b}); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return fmt.Errorf("节点 %s 未响应 %s：%w", l.name, method, ctx.Err())
	case res, ok := <-ch:
		if !ok {
			return fmt.Errorf("节点 %s 已断开", l.name)
		}
		if !res.OK {
			return errors.New(res.Error)
		}
		return nil
	}
}

func (l *remoteLink) resolve(f node.Frame) {
	l.mu.Lock()
	ch := l.pending[f.ID]
	l.mu.Unlock()
	if ch != nil {
		ch <- f
	}
}

func (l *remoteLink) failPending() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	for id, ch := range l.pending {
		close(ch)
		delete(l.pending, id)
	}
}

func (l *remoteLink) RunTurn(ctx context.Context, r node.TurnReq) error {
	return l.call(ctx, "run_turn", r)
}

func (l *remoteLink) Cancel(ctx context.Context, taskID string) error {
	return l.call(ctx, "cancel", node.CancelParams{TaskID: taskID})
}

func (l *remoteLink) Decide(ctx context.Context, id string, allow bool, reason string) error {
	return l.call(ctx, "decide", node.DecideParams{ID: id, Allow: allow, Reason: reason})
}
