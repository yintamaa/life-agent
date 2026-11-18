// Package mail syncs IMAP mailboxes. Each configured account is its own
// connector/source ("mail:<id>"), so any number of mailboxes can be attached
// (Gmail, Outlook, QQ 邮箱, 163, 飞书邮箱, 企业邮箱 … anything with IMAP).
//
// Mails are grouped into conversations by thread (References / In-Reply-To),
// so a thread reads like a chat in the UI. Fetching uses BODY.PEEK and a
// read-only SELECT, so syncing never marks mail as read on the server.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/charset"
	gomail "github.com/emersion/go-message/mail"

	"github.com/yintamaa/life-agent/internal/connector"
	"github.com/yintamaa/life-agent/internal/model"
)

func init() { imap.CharsetReader = charset.Reader }

// Account is one IMAP mailbox.
type Account struct {
	ID       string `yaml:"id"`   // short slug, source becomes "mail:<id>"
	Name     string `yaml:"name"` // display name, e.g. "工作邮箱"
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"` // default 993 (implicit TLS)
	Username string `yaml:"username"`
	// One of: Password (plain, discouraged), PasswordEnv (env var name),
	// PasswordCmd (e.g. `security find-generic-password -w -s life-agent-gmail`).
	Password    string        `yaml:"password"`
	PasswordEnv string        `yaml:"password_env"`
	PasswordCmd string        `yaml:"password_cmd"`
	Folders     []string      `yaml:"folders"` // default ["INBOX"]
	SelfAddrs   []string      `yaml:"self_addresses"`
	Poll        time.Duration `yaml:"poll_interval"`
	Backfill    time.Duration `yaml:"backfill"`
	StartTLS    bool          `yaml:"starttls"` // port 143 + STARTTLS instead of implicit TLS
}

// Connector syncs one Account.
type Connector struct{ a Account }

// New creates a connector for one account.
func New(a Account) *Connector {
	if a.Port == 0 {
		a.Port = 993
	}
	if len(a.Folders) == 0 {
		a.Folders = []string{"INBOX"}
	}
	if a.Poll == 0 {
		a.Poll = 60 * time.Second
	}
	if a.Backfill == 0 {
		a.Backfill = 7 * 24 * time.Hour
	}
	if a.Name == "" {
		a.Name = a.Username
	}
	a.SelfAddrs = append(a.SelfAddrs, a.Username)
	return &Connector{a: a}
}

func (c *Connector) ID() string       { return "mail:" + c.a.ID }
func (c *Connector) Platform() string { return model.PlatformMail }
func (c *Connector) Name() string     { return c.a.Name }

func (c *Connector) password(ctx context.Context) (string, error) {
	switch {
	case c.a.PasswordCmd != "":
		out, err := exec.CommandContext(ctx, "sh", "-c", c.a.PasswordCmd).Output()
		if err != nil {
			return "", fmt.Errorf("password_cmd: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	case c.a.PasswordEnv != "":
		if v := os.Getenv(c.a.PasswordEnv); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("环境变量 %s 为空", c.a.PasswordEnv)
	case c.a.Password != "":
		return c.a.Password, nil
	}
	return "", errors.New("未配置密码（password / password_env / password_cmd）")
}

// Run implements connector.Connector.
func (c *Connector) Run(ctx context.Context, sink connector.Sink, st *connector.Status) error {
	return connector.PollLoop(ctx, st, c.a.Poll, func(ctx context.Context) error {
		return c.sync(ctx, sink)
	})
}

func (c *Connector) dial(ctx context.Context) (*client.Client, error) {
	addr := fmt.Sprintf("%s:%d", c.a.Host, c.a.Port)
	var cl *client.Client
	var err error
	if c.a.StartTLS {
		if cl, err = client.Dial(addr); err == nil {
			err = cl.StartTLS(&tls.Config{ServerName: c.a.Host})
		}
	} else {
		cl, err = client.DialTLS(addr, &tls.Config{ServerName: c.a.Host})
	}
	if err != nil {
		return nil, fmt.Errorf("连接 %s: %w", addr, err)
	}
	cl.Timeout = 60 * time.Second
	pw, err := c.password(ctx)
	if err != nil {
		cl.Logout()
		return nil, err
	}
	if err := cl.Login(c.a.Username, pw); err != nil {
		cl.Logout()
		return nil, fmt.Errorf("登录失败（很多邮箱需要“授权码/应用专用密码”而不是登录密码）：%w", err)
	}
	// Some providers (163/126) require an IMAP ID before SELECT.
	_ = idCommand(cl)
	return cl, nil
}

func (c *Connector) sync(ctx context.Context, sink connector.Sink) error {
	cl, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer cl.Logout()
	var errs []error
	for _, f := range c.a.Folders {
		if err := c.syncFolder(ctx, cl, sink, f); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Connector) syncFolder(ctx context.Context, cl *client.Client, sink connector.Sink, folder string) error {
	mbox, err := cl.Select(folder, true)
	if err != nil {
		return err
	}
	key := "uid:" + folder
	cur, err := sink.Cursor(ctx, c.ID(), key)
	if err != nil {
		return err
	}
	// cursor format: "<uidvalidity>:<last uid>"
	var lastUID uint32
	if v, u, ok := strings.Cut(cur, ":"); ok && v == strconv.FormatUint(uint64(mbox.UidValidity), 10) {
		n, _ := strconv.ParseUint(u, 10, 32)
		lastUID = uint32(n)
	}
	seq := new(imap.SeqSet)
	if lastUID == 0 {
		crit := imap.NewSearchCriteria()
		crit.Since = time.Now().Add(-c.a.Backfill)
		uids, err := cl.UidSearch(crit)
		if err != nil {
			return err
		}
		if len(uids) == 0 {
			return sink.SetCursor(ctx, c.ID(), key, fmt.Sprintf("%d:%d", mbox.UidValidity, max(mbox.UidNext, 1)-1))
		}
		seq.AddNum(uids...)
	} else {
		seq.AddRange(lastUID+1, 0) // 0 == "*"
	}

	section := &imap.BodySectionName{Peek: true, Partial: []int{0, 512 * 1024}}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid, section.FetchItem()}
	ch := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() { done <- cl.UidFetch(seq, items, ch) }()

	maxUID := lastUID
	for msg := range ch {
		if msg.Uid <= lastUID { // "N:*" returns the last message even if < N
			continue
		}
		maxUID = max(maxUID, msg.Uid)
		conv, m := c.normalize(msg, section)
		if lastUID == 0 {
			m.AlreadyRead = true // first sync of this folder = history
		}
		if err := sink.UpsertConversation(ctx, conv); err != nil {
			return err
		}
		if err := sink.IngestMessages(ctx, []model.Message{m}); err != nil {
			return err
		}
	}
	if err := <-done; err != nil {
		return err
	}
	if maxUID > lastUID {
		return sink.SetCursor(ctx, c.ID(), key, fmt.Sprintf("%d:%d", mbox.UidValidity, maxUID))
	}
	return nil
}

var subjPrefix = regexp.MustCompile(`(?i)^\s*((re|fw|fwd|回复|答复|转发)\s*[:：]\s*)+`)

func (c *Connector) normalize(msg *imap.Message, section *imap.BodySectionName) (model.Conversation, model.Message) {
	env := msg.Envelope
	if env == nil {
		env = &imap.Envelope{}
	}
	var refs []string
	body := ""
	if r := msg.GetBody(section); r != nil {
		refs, body = parseBody(r)
	}
	root := env.MessageId
	if len(refs) > 0 {
		root = refs[0]
	} else if env.InReplyTo != "" {
		root = env.InReplyTo
	}
	if root == "" {
		root = "uid-" + strconv.FormatUint(uint64(msg.Uid), 10)
	}
	root = strings.Trim(root, "<> ")
	title := strings.TrimSpace(subjPrefix.ReplaceAllString(env.Subject, ""))
	if title == "" {
		title = "(无主题)"
	}
	conv := model.Conversation{
		ID: model.ConvID(c.ID(), root), Source: c.ID(), Platform: model.PlatformMail,
		NativeID: root, Kind: model.KindMail, Title: title,
	}
	var fromName, fromAddr string
	if len(env.From) > 0 {
		f := env.From[0]
		fromAddr = f.Address()
		fromName = f.PersonalName
		if fromName == "" {
			fromName = fromAddr
		}
	}
	self := false
	for _, a := range c.a.SelfAddrs {
		if a != "" && strings.EqualFold(a, fromAddr) {
			self = true
		}
	}
	seen := false
	for _, f := range msg.Flags {
		if f == imap.SeenFlag {
			seen = true
		}
	}
	mid := env.MessageId
	if mid == "" {
		mid = "uid-" + strconv.FormatUint(uint64(msg.Uid), 10)
	}
	sent := env.Date
	if sent.IsZero() {
		sent = time.Now()
	}
	return conv, model.Message{
		ID:             model.ConvID(c.ID(), strings.Trim(mid, "<> ")),
		ConversationID: conv.ID,
		Source:         c.ID(),
		SenderID:       fromAddr,
		SenderName:     fromName,
		IsSelf:         self,
		Type:           model.TypeMail,
		Subject:        env.Subject,
		Text:           body,
		SentAt:         sent,
		AlreadyRead:    seen,
		IsBot:          looksAutomated(fromAddr),
	}
}

func looksAutomated(addr string) bool {
	a := strings.ToLower(addr)
	for _, p := range []string{"noreply", "no-reply", "notification", "notify", "mailer-daemon", "alert"} {
		if strings.Contains(a, p) {
			return true
		}
	}
	return false
}

var (
	htmlTag   = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]+>`)
	blankRuns = regexp.MustCompile(`\n\s*\n+`)
)

// parseBody returns References ids and a plain-text body (best effort; the
// fetch may be truncated at 512KB so late parse errors are ignored).
func parseBody(r io.Reader) ([]string, string) {
	mr, err := gomail.CreateReader(r)
	if err != nil && mr == nil {
		return nil, ""
	}
	refs, _ := mr.Header.MsgIDList("References")
	var plain, htmlText string
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		h, ok := p.Header.(*gomail.InlineHeader)
		if !ok {
			continue
		}
		ct, _, _ := h.ContentType()
		b, _ := io.ReadAll(io.LimitReader(p.Body, 256*1024))
		switch {
		case ct == "text/plain" && plain == "":
			plain = string(b)
		case ct == "text/html" && htmlText == "":
			htmlText = string(b)
		}
	}
	text := plain
	if strings.TrimSpace(text) == "" && htmlText != "" {
		text = htmlTag.ReplaceAllString(htmlText, "\n")
		text = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`).Replace(text)
	}
	text = strings.TrimSpace(blankRuns.ReplaceAllString(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n"))
	if r := []rune(text); len(r) > 8000 {
		text = string(r[:8000]) + "…"
	}
	return refs, text
}
