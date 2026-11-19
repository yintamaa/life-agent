package node

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"

	"github.com/yintamaa/life-agent/internal/agent"
)

// Node link protocol (JSON text frames over one WebSocket, node dials hub):
//
//	node → hub  {"type":"hello","node":"name","info":{...}}
//	hub  → node {"type":"welcome","acked":N}         // last event seq the hub has persisted
//	node → hub  {"type":"event","seq":N,"kind":"session_event"|"approval_request",...}
//	hub  → node {"type":"ack","seq":N}               // persisted; node drops ≤ N from its outbox
//	hub  → node {"type":"req","id":"…","method":"run_turn"|"cancel"|"decide","params":{…}}
//	node → hub  {"type":"res","id":"…","ok":true,"error":""}
//
// Events go through a durable SQLite outbox, so nothing a node reports is
// lost across disconnects or restarts of either side (at-least-once; the hub
// dedupes by seq). Requests are not queued: if the link is down the hub
// reports the node offline.
type Frame struct {
	Type     string          `json:"type"`
	Node     string          `json:"node,omitempty"`
	Info     *Info           `json:"info,omitempty"`
	Acked    int64           `json:"acked,omitempty"`
	Seq      int64           `json:"seq,omitempty"`
	Kind     string          `json:"kind,omitempty"`
	TaskID   string          `json:"task_id,omitempty"`
	Event    *agent.Event    `json:"event,omitempty"`
	Approval *ApprovalReq    `json:"approval,omitempty"`
	ID       string          `json:"id,omitempty"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	OK       bool            `json:"ok,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// DecideParams is the payload of the "decide" request.
type DecideParams struct {
	ID     string `json:"id"`
	Allow  bool   `json:"allow"`
	Reason string `json:"reason"`
}

// CancelParams is the payload of the "cancel" request.
type CancelParams struct {
	TaskID string `json:"task_id"`
}

// Client connects a Node to a remote hub. It implements Hub for the node.
type Client struct {
	hubURL, token string
	node          *Node
	db            *sql.DB
	kick          chan struct{}
}

// NewClient opens the outbox under dataDir and wires itself as the node's hub.
func NewClient(hubURL, token string, n *Node, dataDir string) (*Client, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "outbox.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS outbox (seq INTEGER PRIMARY KEY AUTOINCREMENT, frame TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	c := &Client{hubURL: wsURL(hubURL), token: token, node: n, db: db, kick: make(chan struct{}, 1)}
	n.SetHub(c)
	return c, nil
}

func wsURL(u string) string {
	u = strings.TrimRight(u, "/")
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	if !strings.HasSuffix(u, "/api/node") {
		u += "/api/node"
	}
	return u
}

func (c *Client) enqueue(ctx context.Context, f Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO outbox (frame) VALUES (?)`, string(b)); err != nil {
		return fmt.Errorf("outbox: %w", err)
	}
	select {
	case c.kick <- struct{}{}:
	default:
	}
	return nil
}

// SessionEvent implements Hub (durably queued).
func (c *Client) SessionEvent(ctx context.Context, taskID string, ev agent.Event) error {
	return c.enqueue(ctx, Frame{Type: "event", Kind: "session_event", TaskID: taskID, Event: &ev})
}

// ApprovalRequest implements Hub (durably queued).
func (c *Client) ApprovalRequest(ctx context.Context, req ApprovalReq) error {
	return c.enqueue(ctx, Frame{Type: "event", Kind: "approval_request", TaskID: req.TaskID, Approval: &req})
}

// Run keeps the link up until ctx is done (exponential backoff 1s → 30s).
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // was healthy for a while
		}
		slog.Warn("hub link down, reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

type wsConn struct {
	*websocket.Conn
	mu sync.Mutex
}

func (w *wsConn) send(f Frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return w.WriteJSON(f)
}

func (w *wsConn) sendRaw(b []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return w.WriteMessage(websocket.TextMessage, b)
}

func (c *Client) session(ctx context.Context) error {
	h := http.Header{}
	if c.token != "" {
		h.Set("Authorization", "Bearer "+c.token)
	}
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	raw, resp, err := d.DialContext(ctx, c.hubURL, h)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial %s: %w (HTTP %d)", c.hubURL, err, resp.StatusCode)
		}
		return fmt.Errorf("dial %s: %w", c.hubURL, err)
	}
	conn := &wsConn{Conn: raw}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	info := c.node.Info(ctx)
	if err := conn.send(Frame{Type: "hello", Node: c.node.Name(), Info: &info}); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	var welcome Frame
	if err := conn.ReadJSON(&welcome); err != nil || welcome.Type != "welcome" {
		return fmt.Errorf("handshake: %v %+v", err, welcome)
	}
	if _, err := c.db.Exec(`DELETE FROM outbox WHERE seq <= ?`, welcome.Acked); err != nil {
		return err
	}
	slog.Info("hub link up", "hub", c.hubURL, "node", c.node.Name(), "hub_acked", welcome.Acked)

	// Liveness: hub pings; we also ping. 60s without any frame = dead.
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	conn.SetPingHandler(func(s string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		conn.mu.Lock()
		defer conn.mu.Unlock()
		return conn.WriteControl(websocket.PongMessage, []byte(s), time.Now().Add(5*time.Second))
	})

	errc := make(chan error, 2)
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { errc <- c.writer(sctx, conn, welcome.Acked) }()
	go func() { errc <- c.reader(sctx, conn) }()
	return <-errc
}

// writer streams outbox frames in seq order.
func (c *Client) writer(ctx context.Context, conn *wsConn, after int64) error {
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	sent := after
	for {
		rows, err := c.db.QueryContext(ctx, `SELECT seq, frame FROM outbox WHERE seq > ? ORDER BY seq LIMIT 200`, sent)
		if err != nil {
			return err
		}
		type item struct {
			seq   int64
			frame string
		}
		var batch []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.seq, &it.frame); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, it)
		}
		rows.Close()
		for _, it := range batch {
			// Inject the outbox seq into the stored frame.
			var f map[string]any
			if err := json.Unmarshal([]byte(it.frame), &f); err != nil {
				return err
			}
			f["seq"] = it.seq
			b, _ := json.Marshal(f)
			if err := conn.sendRaw(b); err != nil {
				return err
			}
			sent = it.seq
		}
		if len(batch) == 200 {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.kick:
		case <-ping.C:
			conn.mu.Lock()
			err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			conn.mu.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

func (c *Client) reader(ctx context.Context, conn *wsConn) error {
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	for {
		var f Frame
		if err := conn.ReadJSON(&f); err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		switch f.Type {
		case "ack":
			if _, err := c.db.Exec(`DELETE FROM outbox WHERE seq <= ?`, f.Seq); err != nil {
				return err
			}
		case "req":
			go func(f Frame) {
				err := c.handle(ctx, f)
				res := Frame{Type: "res", ID: f.ID, OK: err == nil}
				if err != nil {
					res.Error = err.Error()
				}
				if err := conn.send(res); err != nil {
					slog.Warn("send response", "err", err)
				}
			}(f)
		}
	}
}

func (c *Client) handle(ctx context.Context, f Frame) error {
	switch f.Method {
	case "run_turn":
		var r TurnReq
		if err := json.Unmarshal(f.Params, &r); err != nil {
			return err
		}
		return c.node.RunTurn(ctx, r)
	case "cancel":
		var p CancelParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			return err
		}
		return c.node.Cancel(p.TaskID)
	case "decide":
		var p DecideParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			return err
		}
		return c.node.Decide(p.ID, p.Allow, p.Reason)
	}
	return errors.New("unknown method " + f.Method)
}

// Close closes the outbox.
func (c *Client) Close() error { return c.db.Close() }
