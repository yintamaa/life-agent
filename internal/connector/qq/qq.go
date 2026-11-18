// Package qq connects to a OneBot v11 implementation (NapCatQQ recommended)
// over a forward WebSocket and ingests private/group messages in real time.
//
// NapCat logs in as a desktop-class client: QQ only allows one desktop
// session per account, so running it for your main account kicks the normal
// Mac/PC QQ offline. Prefer a dedicated always-on machine/VM, and keep Send
// off unless you accept the account-risk trade-off (see docs/connectors.md).
package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yintamaa/life-agent/internal/connector"
	"github.com/yintamaa/life-agent/internal/model"
)

// Config for the QQ connector.
type Config struct {
	Enabled     bool   `yaml:"enabled"`
	Name        string `yaml:"name"`
	WSURL       string `yaml:"ws_url"` // e.g. ws://127.0.0.1:3001
	AccessToken string `yaml:"access_token"`
	Send        bool   `yaml:"send"`
}

// Connector implements connector.Connector and connector.Sender.
type Connector struct {
	cfg    Config
	selfID int64

	mu      sync.Mutex
	conn    *websocket.Conn
	wmu     sync.Mutex
	pending map[string]chan actionResp
	seq     atomic.Int64

	groups  sync.Map // group_id -> name
	friends sync.Map // user_id -> remark/nickname
}

// New creates the connector.
func New(cfg Config) *Connector {
	if cfg.Name == "" {
		cfg.Name = "QQ"
	}
	if cfg.WSURL == "" {
		cfg.WSURL = "ws://127.0.0.1:3001"
	}
	return &Connector{cfg: cfg, pending: map[string]chan actionResp{}}
}

func (c *Connector) ID() string       { return "qq" }
func (c *Connector) Platform() string { return model.PlatformQQ }
func (c *Connector) Name() string     { return c.cfg.Name }

type actionResp struct {
	Status  string          `json:"status"`
	RetCode int             `json:"retcode"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
	Wording string          `json:"wording"`
	Echo    string          `json:"echo"`
}

// Run keeps a WebSocket session alive, reconnecting with backoff.
func (c *Connector) Run(ctx context.Context, sink connector.Sink, st *connector.Status) error {
	backoff := 2 * time.Second
	for {
		st.Connecting()
		err := c.session(ctx, sink, st)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			st.Fail(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 2*time.Minute)
	}
}

func (c *Connector) session(ctx context.Context, sink connector.Sink, st *connector.Status) error {
	h := http.Header{}
	if c.cfg.AccessToken != "" {
		h.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.cfg.WSURL, h)
	if err != nil {
		return fmt.Errorf("连接 OneBot %s 失败：%w（NapCat 是否在运行？）", c.cfg.WSURL, err)
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		conn.Close()
	}()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	readErr := make(chan error, 1)
	go func() { readErr <- c.readLoop(ctx, conn, sink, st) }()

	if err := c.bootstrap(ctx); err != nil {
		return err
	}
	st.OK()
	return <-readErr
}

func (c *Connector) bootstrap(ctx context.Context) error {
	var login struct {
		UserID int64 `json:"user_id"`
	}
	if err := c.call(ctx, "get_login_info", nil, &login); err != nil {
		return fmt.Errorf("get_login_info: %w", err)
	}
	c.selfID = login.UserID
	var groups []struct {
		GroupID   int64  `json:"group_id"`
		GroupName string `json:"group_name"`
	}
	if err := c.call(ctx, "get_group_list", nil, &groups); err == nil {
		for _, g := range groups {
			c.groups.Store(g.GroupID, g.GroupName)
		}
	}
	var friends []struct {
		UserID   int64  `json:"user_id"`
		Nickname string `json:"nickname"`
		Remark   string `json:"remark"`
	}
	if err := c.call(ctx, "get_friend_list", nil, &friends); err == nil {
		for _, f := range friends {
			n := f.Remark
			if n == "" {
				n = f.Nickname
			}
			c.friends.Store(f.UserID, n)
		}
	}
	return nil
}

func (c *Connector) call(ctx context.Context, action string, params any, out any) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("QQ 未连接")
	}
	echo := strconv.FormatInt(c.seq.Add(1), 10)
	ch := make(chan actionResp, 1)
	c.mu.Lock()
	c.pending[echo] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, echo)
		c.mu.Unlock()
	}()
	if params == nil {
		params = map[string]any{}
	}
	c.wmu.Lock()
	err := conn.WriteJSON(map[string]any{"action": action, "params": params, "echo": echo})
	c.wmu.Unlock()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", action, ctx.Err())
	case r := <-ch:
		if r.Status != "ok" {
			return fmt.Errorf("%s: retcode=%d %s%s", action, r.RetCode, r.Message, r.Wording)
		}
		if out != nil && len(r.Data) > 0 {
			return json.Unmarshal(r.Data, out)
		}
		return nil
	}
}

type event struct {
	PostType    string          `json:"post_type"`
	MessageType string          `json:"message_type"`
	MessageID   json.Number     `json:"message_id"`
	Time        int64           `json:"time"`
	UserID      int64           `json:"user_id"`
	GroupID     int64           `json:"group_id"`
	TargetID    int64           `json:"target_id"`
	RawMessage  string          `json:"raw_message"`
	Message     json.RawMessage `json:"message"`
	Sender      struct {
		Nickname string `json:"nickname"`
		Card     string `json:"card"`
	} `json:"sender"`
	Echo string `json:"echo"`
}

func (c *Connector) readLoop(ctx context.Context, conn *websocket.Conn, sink connector.Sink, st *connector.Status) error {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var probe struct {
			Echo     *string `json:"echo"`
			PostType string  `json:"post_type"`
		}
		if json.Unmarshal(data, &probe) != nil {
			continue
		}
		if probe.Echo != nil && probe.PostType == "" {
			var r actionResp
			if json.Unmarshal(data, &r) == nil {
				c.mu.Lock()
				ch := c.pending[r.Echo]
				c.mu.Unlock()
				if ch != nil {
					ch <- r
				}
			}
			continue
		}
		switch probe.PostType {
		case "meta_event":
			st.OK() // heartbeat keeps "同步 N 秒前" fresh
		case "message", "message_sent":
			var ev event
			if err := json.Unmarshal(data, &ev); err != nil {
				continue
			}
			if err := c.ingest(ctx, sink, ev); err != nil {
				st.Fail(err)
			} else {
				st.OK()
			}
		}
	}
}

func (c *Connector) ingest(ctx context.Context, sink connector.Sink, ev event) error {
	self := ev.PostType == "message_sent" || ev.UserID == c.selfID
	var native, title, kind string
	switch ev.MessageType {
	case "group":
		native, kind = "group:"+strconv.FormatInt(ev.GroupID, 10), model.KindGroup
		if n, ok := c.groups.Load(ev.GroupID); ok {
			title = n.(string)
		} else {
			title = "群 " + strconv.FormatInt(ev.GroupID, 10)
		}
	case "private":
		peer := ev.UserID
		if self && ev.TargetID != 0 {
			peer = ev.TargetID
		}
		native, kind = "private:"+strconv.FormatInt(peer, 10), model.KindP2P
		if n, ok := c.friends.Load(peer); ok {
			title = n.(string)
		} else if !self {
			title = ev.Sender.Nickname
		}
	default:
		return nil
	}
	conv := model.Conversation{
		ID: model.ConvID(c.ID(), native), Source: c.ID(), Platform: c.Platform(),
		NativeID: native, Kind: kind, Title: title,
	}
	if err := sink.UpsertConversation(ctx, conv); err != nil {
		return err
	}
	name := ev.Sender.Card
	if name == "" {
		name = ev.Sender.Nickname
	}
	if n, ok := c.friends.Load(ev.UserID); ok && n.(string) != "" {
		name = n.(string)
	}
	text, typ, mention := c.render(ev)
	ts := time.Unix(ev.Time, 0)
	if ev.Time == 0 {
		ts = time.Now()
	}
	return sink.IngestMessages(ctx, []model.Message{{
		ID:             model.ConvID(c.ID(), ev.MessageID.String()),
		ConversationID: conv.ID,
		Source:         c.ID(),
		SenderID:       strconv.FormatInt(ev.UserID, 10),
		SenderName:     name,
		IsSelf:         self,
		Type:           typ,
		Text:           text,
		SentAt:         ts,
		MentionsMe:     mention,
	}})
}

type segment struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func (c *Connector) render(ev event) (text, typ string, mention bool) {
	var segs []segment
	if err := json.Unmarshal(ev.Message, &segs); err != nil {
		return ev.RawMessage, model.TypeText, false // string post format
	}
	typ = model.TypeText
	var b strings.Builder
	self := strconv.FormatInt(c.selfID, 10)
	for _, s := range segs {
		str := func(k string) string { return fmt.Sprint(s.Data[k]) }
		switch s.Type {
		case "text":
			b.WriteString(str("text"))
		case "at":
			qq := str("qq")
			if qq == self || qq == "all" {
				mention = true
			}
			if qq == "all" {
				b.WriteString("@所有人 ")
			} else if n, ok := s.Data["name"].(string); ok && n != "" {
				b.WriteString("@" + n + " ")
			} else {
				b.WriteString("@" + qq + " ")
			}
		case "face", "mface":
			b.WriteString("[表情]")
		case "image":
			b.WriteString("[图片]")
			if len(segs) == 1 {
				typ = model.TypeImage
			}
		case "record":
			b.WriteString("[语音]")
			typ = model.TypeVoice
		case "video":
			b.WriteString("[视频]")
			typ = model.TypeVideo
		case "file":
			b.WriteString("[文件] " + str("name"))
			typ = model.TypeFile
		case "forward":
			b.WriteString("[聊天记录]")
		case "json":
			b.WriteString("[卡片]")
			typ = model.TypeCard
		case "reply":
			// quoted message; skip
		default:
			b.WriteString("[" + s.Type + "]")
		}
	}
	return strings.TrimSpace(b.String()), typ, mention
}

// Send sends a text message (disabled unless cfg.Send).
func (c *Connector) Send(ctx context.Context, native, text string) error {
	if !c.cfg.Send {
		return connector.ErrSendUnsupported
	}
	kind, id, ok := strings.Cut(native, ":")
	if !ok {
		return fmt.Errorf("bad qq conversation id %q", native)
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return err
	}
	p := map[string]any{"message": text}
	if kind == "group" {
		p["message_type"], p["group_id"] = "group", n
	} else {
		p["message_type"], p["user_id"] = "private", n
	}
	return c.call(ctx, "send_msg", p, nil)
}

// CanSend reports whether sending is enabled in config.
func (c *Connector) CanSend() bool { return c.cfg.Send }
