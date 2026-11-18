// Package wechat reads WeChat 4.x (macOS) chat history from an
// *already-decrypted* copy of the local databases.
//
// This connector deliberately does NOT extract keys or decrypt anything. You
// keep a decrypted mirror of db_storage/ up to date with a tool of your own
// choosing (see docs/connectors.md); this package only reads plain SQLite:
//
//	<db_dir>/session/session.db     SessionTable(username, summary, last_timestamp, ...)
//	<db_dir>/contact/contact.db     contact(username, alias, remark, nick_name), chat_room(username, ext_buffer)
//	<db_dir>/message/message_N.db   Msg_<md5(talker)>(sort_seq, local_type, real_sender_id, create_time, message_content, status), Name2Id
//
// Read-only by design: nothing is ever sent to WeChat.
package wechat

import (
	"bytes"
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/encoding/protowire"
	_ "modernc.org/sqlite"

	"github.com/yintamaa/life-agent/internal/connector"
	"github.com/yintamaa/life-agent/internal/model"
)

// Config for the WeChat connector.
type Config struct {
	Enabled      bool          `yaml:"enabled"`
	Name         string        `yaml:"name"`
	DBDir        string        `yaml:"db_dir"`    // decrypted db_storage mirror
	SelfWxid     string        `yaml:"self_wxid"` // your wxid_xxx, for is_self
	PollInterval time.Duration `yaml:"poll_interval"`
	Backfill     time.Duration `yaml:"backfill"`
	// IncludeOfficialAccounts: also sync gh_* 公众号 sessions (noisy).
	IncludeOfficialAccounts bool `yaml:"include_official_accounts"`
}

// Connector implements connector.Connector (read-only).
type Connector struct {
	cfg         Config
	zd          *zstd.Decoder
	contacts    map[string]string            // username -> best display name
	roomNames   map[string]map[string]string // chatroom -> member -> 群昵称
	contactsAt  time.Time
}

// New creates the connector.
func New(cfg Config) *Connector {
	if cfg.Name == "" {
		cfg.Name = "微信"
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Second
	}
	if cfg.Backfill == 0 {
		cfg.Backfill = 3 * 24 * time.Hour
	}
	zd, _ := zstd.NewReader(nil)
	return &Connector{cfg: cfg, zd: zd}
}

func (c *Connector) ID() string       { return "wechat" }
func (c *Connector) Platform() string { return model.PlatformWeChat }
func (c *Connector) Name() string     { return c.cfg.Name }

// Run implements connector.Connector.
func (c *Connector) Run(ctx context.Context, sink connector.Sink, st *connector.Status) error {
	if c.cfg.DBDir == "" {
		st.Disabled("未配置 wechat.db_dir（解密后的 db_storage 目录）")
		<-ctx.Done()
		return ctx.Err()
	}
	return connector.PollLoop(ctx, st, c.cfg.PollInterval, func(ctx context.Context) error {
		return c.sync(ctx, sink)
	})
}

func openRO(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(3000)")
}

var msgDBRe = regexp.MustCompile(`^message_\d+\.db$`)

func (c *Connector) files() (session, contact string, messages []string, err error) {
	err = filepath.WalkDir(c.cfg.DBDir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		switch n := d.Name(); {
		case n == "session.db":
			session = p
		case n == "contact.db":
			contact = p
		case msgDBRe.MatchString(n):
			messages = append(messages, p)
		}
		return nil
	})
	if err == nil && session == "" {
		err = fmt.Errorf("在 %s 下没找到 session.db，确认目录是解密后的 db_storage", c.cfg.DBDir)
	}
	return
}

func (c *Connector) sync(ctx context.Context, sink connector.Sink) error {
	sessionDB, contactDB, msgDBs, err := c.files()
	if err != nil {
		return err
	}
	if contactDB != "" && time.Since(c.contactsAt) > 10*time.Minute {
		if err := c.loadContacts(ctx, contactDB); err != nil {
			return fmt.Errorf("contact.db: %w", err)
		}
	}

	cur, err := sink.Cursor(ctx, c.ID(), "session_ts")
	if err != nil {
		return err
	}
	since, _ := strconv.ParseInt(cur, 10, 64)
	backfill := since == 0 // first run: history, mark as read
	if backfill {
		since = time.Now().Add(-c.cfg.Backfill).Unix()
	}
	sdb, err := openRO(sessionDB)
	if err != nil {
		return err
	}
	defer sdb.Close()
	rows, err := sdb.QueryContext(ctx, `SELECT username, last_timestamp FROM SessionTable WHERE last_timestamp >= ? ORDER BY last_timestamp ASC`, since)
	if err != nil {
		return fmt.Errorf("session.db: %w", err)
	}
	type sess struct {
		user string
		ts   int64
	}
	var sessions []sess
	for rows.Next() {
		var s sess
		if err := rows.Scan(&s.user, &s.ts); err != nil {
			rows.Close()
			return err
		}
		if c.skip(s.user) {
			continue
		}
		sessions = append(sessions, s)
	}
	rows.Close()

	dbs := make([]*sql.DB, 0, len(msgDBs))
	for _, p := range msgDBs {
		db, err := openRO(p)
		if err != nil {
			continue
		}
		defer db.Close()
		dbs = append(dbs, db)
	}

	maxTS := since
	var errs []error
	for _, s := range sessions {
		kind := model.KindP2P
		if strings.HasSuffix(s.user, "@chatroom") {
			kind = model.KindGroup
		}
		conv := model.Conversation{
			ID: model.ConvID(c.ID(), s.user), Source: c.ID(), Platform: c.Platform(),
			NativeID: s.user, Kind: kind, Title: c.displayName(s.user),
		}
		if err := sink.UpsertConversation(ctx, conv); err != nil {
			return err
		}
		if err := c.syncTalker(ctx, sink, dbs, conv, backfill); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", conv.Title, err))
			continue
		}
		maxTS = max(maxTS, s.ts)
	}
	if len(errs) == 0 && maxTS > since {
		// -1 so same-second updates are re-checked; message cursors dedupe.
		if err := sink.SetCursor(ctx, c.ID(), "session_ts", strconv.FormatInt(maxTS-1, 10)); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

func (c *Connector) skip(user string) bool {
	switch {
	case user == "", strings.HasPrefix(user, "@placeholder"), user == "brandsessionholder",
		user == "brandservicesessionholder", user == "notifymessage", user == "newsapp":
		return true
	case strings.HasPrefix(user, "gh_"):
		return !c.cfg.IncludeOfficialAccounts
	}
	return false
}

func (c *Connector) syncTalker(ctx context.Context, sink connector.Sink, dbs []*sql.DB, conv model.Conversation, backfill bool) error {
	talker := conv.NativeID
	key := "seq:" + talker
	cur, err := sink.Cursor(ctx, c.ID(), key)
	if err != nil {
		return err
	}
	seq, _ := strconv.ParseInt(cur, 10, 64)
	if seq == 0 {
		seq = time.Now().Add(-c.cfg.Backfill).Unix() * 1000 // sort_seq = unix*1000 + n
	}
	sum := md5.Sum([]byte(talker))
	table := "Msg_" + hex.EncodeToString(sum[:])
	maxSeq := seq
	var msgs []model.Message
	for _, db := range dbs {
		var one int
		if err := db.QueryRowContext(ctx, `SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&one); err != nil {
			continue
		}
		rows, err := db.QueryContext(ctx, fmt.Sprintf(`
SELECT m.sort_seq, m.local_type, IFNULL(n.user_name, ''), m.create_time, m.message_content, m.status
FROM %s m LEFT JOIN Name2Id n ON m.real_sender_id = n.rowid
WHERE m.sort_seq > ? ORDER BY m.sort_seq ASC LIMIT 2000`, table), seq)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r rawMsg
			if err := rows.Scan(&r.seq, &r.localType, &r.sender, &r.createTime, &r.content, &r.status); err != nil {
				rows.Close()
				return err
			}
			maxSeq = max(maxSeq, r.seq)
			m := c.normalize(conv, r)
			m.AlreadyRead = backfill
			msgs = append(msgs, m)
		}
		rows.Close()
	}
	if err := sink.IngestMessages(ctx, msgs); err != nil {
		return err
	}
	if maxSeq > seq {
		return sink.SetCursor(ctx, c.ID(), key, strconv.FormatInt(maxSeq, 10))
	}
	return nil
}

type rawMsg struct {
	seq, localType, createTime int64
	sender                     string
	content                    []byte
	status                     int
}

var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

func (c *Connector) normalize(conv model.Conversation, r rawMsg) model.Message {
	content := r.content
	if bytes.HasPrefix(content, zstdMagic) {
		if b, err := c.zd.DecodeAll(content, nil); err == nil {
			content = b
		}
	}
	text := string(content)
	sender := r.sender
	if conv.Kind == model.KindGroup {
		if i := strings.Index(text, ":\n"); i > 0 && !strings.ContainsAny(text[:i], " <\n") {
			sender, text = text[:i], text[i+2:]
		}
	}
	isSelf := r.status == 2
	if c.cfg.SelfWxid != "" && sender != "" {
		isSelf = sender == c.cfg.SelfWxid
	}
	m := model.Message{
		ID:             model.ConvID(c.ID(), conv.NativeID+"/"+strconv.FormatInt(r.seq, 10)),
		ConversationID: conv.ID,
		Source:         c.ID(),
		SenderID:       sender,
		SenderName:     c.memberName(conv.NativeID, sender),
		IsSelf:         isSelf,
		SentAt:         time.Unix(r.createTime, 0),
	}
	if conv.Kind == model.KindP2P && !isSelf && m.SenderName == "" {
		m.SenderName = conv.Title
	}
	m.Type, m.Text = render(r.localType&0xFFFFFFFF, text)
	return m
}

var (
	titleRe   = regexp.MustCompile(`(?s)<title>(?:<!\[CDATA\[)?(.*?)(?:\]\]>)?</title>`)
	appTypeRe = regexp.MustCompile(`<type>(\d+)</type>`)
	tagRe     = regexp.MustCompile(`<[^>]+>`)
)

func render(t int64, s string) (string, string) {
	switch t {
	case 1:
		return model.TypeText, s
	case 3:
		return model.TypeImage, "[图片]"
	case 34:
		return model.TypeVoice, "[语音]"
	case 42:
		return model.TypeLink, "[名片]"
	case 43:
		return model.TypeVideo, "[视频]"
	case 47:
		return model.TypeImage, "[表情]"
	case 48:
		return model.TypeLink, "[位置]"
	case 50:
		return model.TypeSystem, "[语音/视频通话]"
	case 49:
		title := ""
		if mm := titleRe.FindStringSubmatch(s); mm != nil {
			title = html.UnescapeString(strings.TrimSpace(mm[1]))
		}
		sub := ""
		if mm := appTypeRe.FindStringSubmatch(s); mm != nil {
			sub = mm[1]
		}
		switch sub {
		case "6":
			return model.TypeFile, "[文件] " + title
		case "57":
			return model.TypeText, title // 引用回复：title 是回复内容
		case "19":
			return model.TypeOther, "[聊天记录] " + title
		case "2000":
			return model.TypeOther, "[转账]"
		case "2001":
			return model.TypeOther, "[红包] " + title
		case "33", "36":
			return model.TypeLink, "[小程序] " + title
		}
		return model.TypeLink, "[链接] " + title
	case 10000, 10002:
		return model.TypeSystem, strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(s, "")))
	}
	if strings.HasPrefix(strings.TrimSpace(s), "<") {
		return model.TypeOther, "[消息]"
	}
	return model.TypeOther, s
}

func (c *Connector) displayName(user string) string {
	if n := c.contacts[user]; n != "" {
		return n
	}
	if user == "filehelper" {
		return "文件传输助手"
	}
	return user
}

func (c *Connector) memberName(room, user string) string {
	if user == "" {
		return ""
	}
	if n := c.contacts[user]; n != "" && !strings.HasPrefix(n, "wxid_") {
		return n // your remark wins over 群昵称
	}
	if n := c.roomNames[room][user]; n != "" {
		return n
	}
	return c.displayName(user)
}

func (c *Connector) loadContacts(ctx context.Context, path string) error {
	db, err := openRO(path)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT username, IFNULL(alias,''), IFNULL(remark,''), IFNULL(nick_name,'') FROM contact`)
	if err != nil {
		return err
	}
	contacts := map[string]string{}
	for rows.Next() {
		var u, alias, remark, nick string
		if err := rows.Scan(&u, &alias, &remark, &nick); err != nil {
			rows.Close()
			return err
		}
		for _, n := range []string{remark, nick, alias} {
			if n != "" {
				contacts[u] = n
				break
			}
		}
	}
	rows.Close()

	rooms := map[string]map[string]string{}
	if rr, err := db.QueryContext(ctx, `SELECT username, ext_buffer FROM chat_room`); err == nil {
		for rr.Next() {
			var room string
			var buf []byte
			if rr.Scan(&room, &buf) == nil && len(buf) > 0 {
				rooms[room] = parseRoomData(buf)
			}
		}
		rr.Close()
	}
	c.contacts, c.roomNames, c.contactsAt = contacts, rooms, time.Now()
	return nil
}

// parseRoomData decodes RoomData{ repeated RoomDataUser users = 1 } where
// RoomDataUser{ string userName = 1; string displayName = 2 }.
func parseRoomData(b []byte) map[string]string {
	out := map[string]string{}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return out
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			user, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return out
			}
			b = b[m:]
			var name, disp string
			for len(user) > 0 {
				f, t, k := protowire.ConsumeTag(user)
				if k < 0 {
					break
				}
				user = user[k:]
				if t == protowire.BytesType {
					v, k2 := protowire.ConsumeBytes(user)
					if k2 < 0 {
						break
					}
					user = user[k2:]
					switch f {
					case 1:
						name = string(v)
					case 2:
						disp = string(v)
					}
					continue
				}
				k2 := protowire.ConsumeFieldValue(f, t, user)
				if k2 < 0 {
					break
				}
				user = user[k2:]
			}
			if name != "" && disp != "" {
				out[name] = disp
			}
			continue
		}
		m := protowire.ConsumeFieldValue(num, typ, b)
		if m < 0 {
			return out
		}
		b = b[m:]
	}
	return out
}
