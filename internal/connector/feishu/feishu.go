// Package feishu syncs Feishu/Lark chats as the logged-in *user* (not a bot),
// by driving the official lark-cli. lark-cli owns OAuth and token refresh, so
// this connector never touches credentials.
//
// Required user scopes (lark-cli auth login --scope ...):
//
//	im:chat:read im:message:readonly im:message.p2p_msg:get_as_user
//	im:message.group_msg:get_as_user contact:user.base:readonly
//	im:message (only for sending)
//
// Sync strategy: chats are listed sorted by activity. A chat that received a
// message jumps to the top, so each round only fetches messages for the top
// N chats plus any chat that moved up since last round; every SweepEvery
// rounds the whole list is swept to catch stragglers.
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yintamaa/life-agent/internal/connector"
	"github.com/yintamaa/life-agent/internal/model"
)

// Config for the Feishu connector.
type Config struct {
	Enabled      bool          `yaml:"enabled"`
	Name         string        `yaml:"name"`
	CLIPath      string        `yaml:"cli_path"`
	PollInterval time.Duration `yaml:"poll_interval"`
	ChatLimit    int           `yaml:"chat_limit"` // how many most-active chats to track
	HotChats     int           `yaml:"hot_chats"`  // always fetched each round
	SweepEvery   int           `yaml:"sweep_every"`
	Backfill     time.Duration `yaml:"backfill"`
	Send         bool          `yaml:"send"`
}

// Connector implements connector.Connector and connector.Sender.
type Connector struct {
	cfg     Config
	selfID  string
	names   sync.Map // open_id -> display name
	members sync.Map // chat_id -> true once its member list was loaded
	learned sync.Map // open_id -> name learned this round, to back-fill
	lastPos map[string]int
	round   int
	// initialized is false until the first full round completes; messages
	// fetched before that are history backfill and are marked already-read.
	initialized bool
}

// New creates the connector with defaults applied.
func New(cfg Config) *Connector {
	if cfg.CLIPath == "" {
		cfg.CLIPath = "lark-cli"
	}
	if cfg.Name == "" {
		cfg.Name = "飞书"
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.ChatLimit == 0 {
		cfg.ChatLimit = 100
	}
	if cfg.HotChats == 0 {
		cfg.HotChats = 10
	}
	if cfg.SweepEvery == 0 {
		cfg.SweepEvery = 10
	}
	if cfg.Backfill == 0 {
		cfg.Backfill = 3 * 24 * time.Hour
	}
	return &Connector{cfg: cfg, lastPos: map[string]int{}}
}

func (c *Connector) ID() string       { return "feishu" }
func (c *Connector) Platform() string { return model.PlatformFeishu }
func (c *Connector) Name() string     { return c.cfg.Name }

type cliResp struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	} `json:"error"`
}

func (c *Connector) cli(ctx context.Context, out any, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.cfg.CLIPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var r cliResp
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		if runErr != nil {
			return fmt.Errorf("lark-cli %s: %v: %s", args[0], runErr, strings.TrimSpace(stderr.String()))
		}
		return fmt.Errorf("lark-cli %s: bad json: %w", args[0], err)
	}
	if !r.OK {
		if r.Error != nil {
			return fmt.Errorf("lark-cli: %s (%s)", r.Error.Message, r.Error.Type)
		}
		return errors.New("lark-cli: request failed")
	}
	if out != nil {
		return json.Unmarshal(r.Data, out)
	}
	return nil
}

func (c *Connector) api(ctx context.Context, out any, method, path string, params map[string]any) error {
	p, _ := json.Marshal(params)
	return c.cli(ctx, out, "api", method, path, "--as", "user", "--params", string(p))
}

// Run implements connector.Connector.
func (c *Connector) Run(ctx context.Context, sink connector.Sink, st *connector.Status) error {
	if _, err := exec.LookPath(c.cfg.CLIPath); err != nil {
		st.Disabled("未找到 lark-cli，请先安装并 lark-cli auth login")
		<-ctx.Done()
		return ctx.Err()
	}
	return connector.PollLoop(ctx, st, c.cfg.PollInterval, func(ctx context.Context) error {
		if c.selfID == "" {
			if err := c.loadSelf(ctx); err != nil {
				return err
			}
			v, err := sink.Cursor(ctx, c.ID(), "init")
			if err != nil {
				return err
			}
			c.initialized = v != ""
		}
		return c.sync(ctx, sink)
	})
}

func (c *Connector) loadSelf(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, c.cfg.CLIPath, "auth", "status")
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("lark-cli auth status: %w", err)
	}
	var s struct {
		Identities struct {
			User struct {
				Available bool   `json:"available"`
				OpenID    string `json:"openId"`
				UserName  string `json:"userName"`
				Message   string `json:"message"`
			} `json:"user"`
		} `json:"identities"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("parse auth status: %w", err)
	}
	u := s.Identities.User
	if !u.Available || u.OpenID == "" {
		return fmt.Errorf("飞书用户身份不可用：%s（运行 lark-cli auth login）", u.Message)
	}
	c.selfID = u.OpenID
	c.names.Store(u.OpenID, u.UserName)
	return nil
}

type chat struct {
	ChatID      string `json:"chat_id"`
	ChatMode    string `json:"chat_mode"`
	Name        string `json:"name"`
	Avatar      string `json:"avatar"`
	P2PTargetID string `json:"p2p_target_id"`
}

func (c *Connector) listChats(ctx context.Context) ([]chat, error) {
	var all []chat
	token := ""
	for len(all) < c.cfg.ChatLimit {
		args := []string{"im", "+chat-list", "--as", "user", "--types", "p2p,group", "--sort", "active_time", "--page-size", "100"}
		if token != "" {
			args = append(args, "--page-token", token)
		}
		var d struct {
			Chats     []chat `json:"chats"`
			HasMore   bool   `json:"has_more"`
			PageToken string `json:"page_token"`
		}
		if err := c.cli(ctx, &d, args...); err != nil {
			return nil, err
		}
		all = append(all, d.Chats...)
		if !d.HasMore || d.PageToken == "" {
			break
		}
		token = d.PageToken
	}
	if len(all) > c.cfg.ChatLimit {
		all = all[:c.cfg.ChatLimit]
	}
	return all, nil
}

func (c *Connector) sync(ctx context.Context, sink connector.Sink) error {
	chats, err := c.listChats(ctx)
	if err != nil {
		return err
	}
	c.round++
	sweep := c.round == 1 || c.round%c.cfg.SweepEvery == 0
	pos := make(map[string]int, len(chats))
	var (
		errs []error
		emu  sync.Mutex
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 4) // parallel lark-cli calls
	)
	for i, ch := range chats {
		pos[ch.ChatID] = i
		kind := model.KindGroup
		if ch.ChatMode == "p2p" {
			kind = model.KindP2P
		}
		conv := model.Conversation{
			ID: model.ConvID(c.ID(), ch.ChatID), Source: c.ID(), Platform: c.Platform(),
			NativeID: ch.ChatID, Kind: kind, Title: ch.Name, Avatar: ch.Avatar,
		}
		if err := sink.UpsertConversation(ctx, conv); err != nil {
			return err
		}
		prev, seen := c.lastPos[ch.ChatID]
		if !(sweep || i < c.cfg.HotChats || !seen || i < prev) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(conv model.Conversation) {
			defer func() { <-sem; wg.Done() }()
			if err := c.syncChat(ctx, sink, conv); err != nil {
				emu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", conv.Title, err))
				emu.Unlock()
			}
		}(conv)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	learned := map[string]string{}
	c.learned.Range(func(k, v any) bool {
		learned[k.(string)] = v.(string)
		c.learned.Delete(k)
		return true
	})
	if err := sink.LearnNames(ctx, c.ID(), learned); err != nil {
		errs = append(errs, err)
	}
	c.lastPos = pos
	if len(errs) == 0 && !c.initialized {
		c.initialized = true
		if err := sink.SetCursor(ctx, c.ID(), "init", "1"); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

type message struct {
	MessageID  string `json:"message_id"`
	MsgType    string `json:"msg_type"`
	CreateTime string `json:"create_time"`
	Deleted    bool   `json:"deleted"`
	Body       struct {
		Content string `json:"content"`
	} `json:"body"`
	Sender struct {
		ID         string `json:"id"`
		SenderType string `json:"sender_type"`
	} `json:"sender"`
	Mentions []struct {
		ID   string `json:"id"`
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"mentions"`
}

func (c *Connector) syncChat(ctx context.Context, sink connector.Sink, conv model.Conversation) error {
	key := "chat:" + conv.NativeID
	cur, err := sink.Cursor(ctx, c.ID(), key)
	if err != nil {
		return err
	}
	startMS, _ := strconv.ParseInt(cur, 10, 64)
	if startMS == 0 {
		startMS = time.Now().Add(-c.cfg.Backfill).UnixMilli()
	}
	token := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{
			"container_id_type": "chat", "container_id": conv.NativeID,
			"start_time": strconv.FormatInt(startMS/1000, 10), // seconds, inclusive; dupes are ignored
			"sort_type":  "ByCreateTimeAsc", "page_size": 50,
		}
		if token != "" {
			params["page_token"] = token
		}
		var d struct {
			Items     []message `json:"items"`
			HasMore   bool      `json:"has_more"`
			PageToken string    `json:"page_token"`
		}
		if err := c.api(ctx, &d, "GET", "/open-apis/im/v1/messages", params); err != nil {
			return err
		}
		c.resolveNames(ctx, conv.NativeID, d.Items)
		msgs := make([]model.Message, 0, len(d.Items))
		maxMS := startMS
		for _, it := range d.Items {
			t, _ := strconv.ParseInt(it.CreateTime, 10, 64)
			maxMS = max(maxMS, t)
			if it.Deleted {
				continue
			}
			m := c.normalize(conv, it, t)
			m.AlreadyRead = !c.initialized
			msgs = append(msgs, m)
		}
		if err := sink.IngestMessages(ctx, msgs); err != nil {
			return err
		}
		if maxMS > startMS {
			if err := sink.SetCursor(ctx, c.ID(), key, strconv.FormatInt(maxMS, 10)); err != nil {
				return err
			}
		}
		if !d.HasMore || d.PageToken == "" {
			return nil
		}
		token = d.PageToken
	}
	return nil
}

// resolveNames resolves unknown user senders: first via the contact API, then
// (for users outside your contact visibility, common in big tenants) via the
// chat's member list, loaded once per chat.
func (c *Connector) resolveNames(ctx context.Context, chatID string, items []message) {
	var ids []string
	seen := map[string]bool{}
	for _, it := range items {
		for _, m := range it.Mentions {
			c.learn(m.ID, m.Name)
		}
		if it.Sender.SenderType != "user" || seen[it.Sender.ID] {
			continue
		}
		if _, ok := c.names.Load(it.Sender.ID); !ok {
			ids = append(ids, it.Sender.ID)
			seen[it.Sender.ID] = true
		}
	}
	for len(ids) > 0 {
		n := min(len(ids), 50)
		var d struct {
			Items []struct {
				OpenID string `json:"open_id"`
				Name   string `json:"name"`
			} `json:"items"`
		}
		if err := c.api(ctx, &d, "GET", "/open-apis/contact/v3/users/batch", map[string]any{"user_ids": ids[:n], "user_id_type": "open_id"}); err == nil {
			for _, u := range d.Items {
				c.names.Store(u.OpenID, u.Name)
			}
		}
		ids = ids[n:]
	}
	for _, it := range items {
		if it.Sender.SenderType != "user" {
			continue
		}
		if _, ok := c.names.Load(it.Sender.ID); !ok {
			c.loadMembers(ctx, chatID)
			return
		}
	}
}

func (c *Connector) learn(id, name string) {
	if id == "" || name == "" {
		return
	}
	if _, loaded := c.names.LoadOrStore(id, name); !loaded {
		c.learned.Store(id, name)
	}
}

func (c *Connector) loadMembers(ctx context.Context, chatID string) {
	if _, done := c.members.LoadOrStore(chatID, true); done {
		return
	}
	token := ""
	for page := 0; page < 50; page++ { // up to 5000 members
		params := map[string]any{"member_id_type": "open_id", "page_size": 100}
		if token != "" {
			params["page_token"] = token
		}
		var d struct {
			Items []struct {
				MemberID string `json:"member_id"`
				Name     string `json:"name"`
			} `json:"items"`
			HasMore   bool   `json:"has_more"`
			PageToken string `json:"page_token"`
		}
		if err := c.api(ctx, &d, "GET", "/open-apis/im/v1/chats/"+chatID+"/members", params); err != nil {
			return
		}
		for _, m := range d.Items {
			c.learn(m.MemberID, m.Name)
		}
		if !d.HasMore || d.PageToken == "" {
			return
		}
		token = d.PageToken
	}
}

func (c *Connector) normalize(conv model.Conversation, it message, t int64) model.Message {
	m := model.Message{
		ID:             model.ConvID(c.ID(), it.MessageID),
		ConversationID: conv.ID,
		Source:         c.ID(),
		SenderID:       it.Sender.ID,
		IsSelf:         it.Sender.ID == c.selfID,
		IsBot:          it.Sender.SenderType != "user",
		SentAt:         time.UnixMilli(t),
	}
	if n, ok := c.names.Load(it.Sender.ID); ok {
		m.SenderName = n.(string)
	} else if m.IsBot {
		m.SenderName = "机器人"
	}
	for _, mt := range it.Mentions {
		if mt.ID == c.selfID {
			m.MentionsMe = true
		}
	}
	m.Type, m.Text = renderContent(it.MsgType, it.Body.Content)
	for _, mt := range it.Mentions {
		m.Text = strings.ReplaceAll(m.Text, mt.Key, "@"+mt.Name)
	}
	m.Text = strings.ReplaceAll(m.Text, "@_all", "@所有人")
	return m
}

// renderContent turns Feishu message content JSON into (type, plain text).
func renderContent(msgType, raw string) (string, string) {
	var v map[string]any
	_ = json.Unmarshal([]byte(raw), &v)
	str := func(k string) string { s, _ := v[k].(string); return s }
	switch msgType {
	case "text":
		return model.TypeText, str("text")
	case "post":
		return model.TypeText, strings.TrimSpace(str("title") + "\n" + collectText(v["content"]))
	case "interactive":
		return model.TypeCard, collectText(v)
	case "image":
		return model.TypeImage, "[图片]"
	case "file":
		return model.TypeFile, "[文件] " + str("file_name")
	case "audio":
		return model.TypeVoice, "[语音]"
	case "media":
		return model.TypeVideo, "[视频] " + str("file_name")
	case "sticker":
		return model.TypeImage, "[表情]"
	case "share_chat":
		return model.TypeLink, "[群名片]"
	case "share_user":
		return model.TypeLink, "[个人名片]"
	case "merge_forward":
		return model.TypeOther, "[合并转发]"
	case "system":
		return model.TypeSystem, "[系统消息]"
	case "todo":
		return model.TypeCard, "[任务] " + collectText(v)
	}
	if t := collectText(v); t != "" {
		return model.TypeOther, t
	}
	return model.TypeOther, "[" + msgType + "]"
}

// collectText walks arbitrary card/post JSON and joins human-readable strings.
func collectText(v any) string {
	var parts []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for _, k := range []string{"title", "text", "content", "user_name"} {
				if s, ok := t[k].(string); ok && s != "" && !strings.HasPrefix(s, "img_") {
					parts = append(parts, s)
				}
			}
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys) // deterministic order for card text
			for _, k := range keys {
				if _, isStr := t[k].(string); !isStr {
					walk(t[k])
				}
			}
		case []any:
			for _, sub := range t {
				walk(sub)
			}
		}
	}
	walk(v)
	return strings.Join(dedupe(parts), " ")
}

func dedupe(ss []string) []string {
	seen := map[string]bool{}
	out := ss[:0]
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Send posts a text message as the user.
func (c *Connector) Send(ctx context.Context, chatID, text string) error {
	if !c.cfg.Send {
		return connector.ErrSendUnsupported
	}
	return c.cli(ctx, nil, "im", "+messages-send", "--as", "user", "--chat-id", chatID, "--text", text)
}

// CanSend reports whether sending is enabled in config.
func (c *Connector) CanSend() bool { return c.cfg.Send }
