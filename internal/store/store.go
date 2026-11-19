// Package store is the hub's SQLite persistence: conversations, messages,
// sync cursors and an FTS5 (trigram, CJK-friendly) full-text index.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo

	"github.com/yintamaa/life-agent/internal/model"
	"github.com/yintamaa/life-agent/internal/triage"
)

const schema = `
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  platform TEXT NOT NULL,
  native_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  avatar TEXT NOT NULL DEFAULT '',
  last_message_at INTEGER NOT NULL DEFAULT 0,
  last_preview TEXT NOT NULL DEFAULT '',
  last_sender TEXT NOT NULL DEFAULT '',
  unread INTEGER NOT NULL DEFAULT 0,
  mentions INTEGER NOT NULL DEFAULT 0,
  needs_attention INTEGER NOT NULL DEFAULT 0,
  title_alert INTEGER NOT NULL DEFAULT 0,
  alert_hits INTEGER NOT NULL DEFAULT 0,
  msg_count INTEGER NOT NULL DEFAULT 0,
  last_read_at INTEGER NOT NULL DEFAULT 0,
  change_seq INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS conv_last ON conversations(last_message_at DESC);
CREATE INDEX IF NOT EXISTS conv_seq ON conversations(change_seq);

CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL,
  source TEXT NOT NULL,
  sender_id TEXT NOT NULL DEFAULT '',
  sender_name TEXT NOT NULL DEFAULT '',
  is_self INTEGER NOT NULL DEFAULT 0,
  type TEXT NOT NULL DEFAULT 'text',
  text TEXT NOT NULL DEFAULT '',
  subject TEXT NOT NULL DEFAULT '',
  sent_at INTEGER NOT NULL,
  mentions_me INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS msg_conv_time ON messages(conversation_id, sent_at);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
  text, subject, sender_name, content='messages', content_rowid='rowid', tokenize='trigram'
);
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, text, subject, sender_name) VALUES (new.rowid, new.text, new.subject, new.sender_name);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text, subject, sender_name) VALUES ('delete', old.rowid, old.text, old.subject, old.sender_name);
END;

CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text, subject, sender_name) VALUES ('delete', old.rowid, old.text, old.subject, old.sender_name);
  INSERT INTO messages_fts(rowid, text, subject, sender_name) VALUES (new.rowid, new.text, new.subject, new.sender_name);
END;
CREATE INDEX IF NOT EXISTS msg_unnamed ON messages(source, sender_id) WHERE sender_name = '';

CREATE TABLE IF NOT EXISTS client_ops (
  key TEXT PRIMARY KEY,
  status INTEGER NOT NULL DEFAULT 0,
  body TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cursors (
  source TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (source, key)
);
`

// Store implements connector.Sink plus the read queries used by the API.
type Store struct {
	db  *sql.DB
	tri *triage.Engine
	wmu sync.Mutex // serialize writers; SQLite has one writer anyway
	// seq is the hub-wide change sequence. Every visible change to a
	// conversation stamps it with the next value, so clients can ask for
	// "everything since N" after a disconnect.
	seq      atomic.Int64
	OnChange func(source string)
}

// ErrStale is returned when a client acts on a conversation that has
// received new messages since the version the client saw.
var ErrStale = errors.New("会话在你查看之后有新消息")

// Open opens (and migrates) the database at path.
func Open(path string, tri *triage.Engine) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	s := &Store{db: db, tri: tri}
	if err := s.migrateTasks(); err != nil {
		return nil, fmt.Errorf("migrate tasks: %w", err)
	}
	cur, err := s.CommittedSeq(context.Background())
	if err != nil {
		return nil, err
	}
	s.seq.Store(cur)
	// Idempotency receipts are kept for 30 days.
	_, _ = db.Exec(`DELETE FROM client_ops WHERE created_at < ?`, time.Now().Add(-30*24*time.Hour).UnixMilli())
	return s, nil
}

// Seq returns the in-memory change counter (may include uncommitted writes).
func (s *Store) Seq() int64 { return s.seq.Load() }

// CommittedSeq returns the highest committed change sequence.
func (s *Store) CommittedSeq(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(
  (SELECT IFNULL(MAX(change_seq), 0) FROM conversations),
  (SELECT IFNULL(MAX(change_seq), 0) FROM tasks))`).Scan(&v)
	return v, err
}

func (s *Store) nextSeq() int64 { return s.seq.Add(1) }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) changed(source string) {
	if s.OnChange != nil {
		s.OnChange(source)
	}
}

// UpsertConversation creates or refreshes conversation metadata. Empty
// title/avatar never overwrite known values.
func (s *Store) UpsertConversation(ctx context.Context, c model.Conversation) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	// Connectors re-upsert every poll round; only real changes bump change_seq.
	res, err := s.db.ExecContext(ctx, `
INSERT INTO conversations (id, source, platform, native_id, kind, title, avatar, title_alert, change_seq)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  kind = excluded.kind,
  title = CASE WHEN excluded.title != '' THEN excluded.title ELSE conversations.title END,
  avatar = CASE WHEN excluded.avatar != '' THEN excluded.avatar ELSE conversations.avatar END,
  title_alert = CASE WHEN excluded.title != '' THEN excluded.title_alert ELSE conversations.title_alert END,
  change_seq = excluded.change_seq
WHERE conversations.kind != excluded.kind
   OR (excluded.title != '' AND conversations.title != excluded.title)
   OR (excluded.avatar != '' AND conversations.avatar != excluded.avatar)`,
		c.ID, c.Source, c.Platform, c.NativeID, c.Kind, c.Title, c.Avatar, b2i(s.tri.ConversationIsAlert(c)), s.seq.Load()+1)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.nextSeq()
		s.changed(c.Source)
	}
	return nil
}

// IngestMessages stores messages idempotently and updates conversation
// aggregates (last message, unread, mentions, needs-attention, alert).
func (s *Store) IngestMessages(ctx context.Context, msgs []model.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	now := time.Now()
	sources := map[string]bool{}
	for _, m := range msgs {
		s.tri.DetectMention(&m)
		res, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO messages (id, conversation_id, source, sender_id, sender_name, is_self, type, text, subject, sent_at, mentions_me)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.ID, m.ConversationID, m.Source, m.SenderID, m.SenderName, b2i(m.IsSelf), m.Type, m.Text, m.Subject, ms(m.SentAt), b2i(m.MentionsMe))
		if err != nil {
			return fmt.Errorf("insert message %s: %w", m.ID, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // duplicate
		}
		var c model.Conversation
		var lastAt, lastRead int64
		err = tx.QueryRowContext(ctx, `SELECT id, source, platform, native_id, kind, title, last_message_at, last_read_at FROM conversations WHERE id = ?`, m.ConversationID).
			Scan(&c.ID, &c.Source, &c.Platform, &c.NativeID, &c.Kind, &c.Title, &lastAt, &lastRead)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("message %s references unknown conversation %s (connector must upsert it first)", m.ID, m.ConversationID)
		} else if err != nil {
			return err
		}
		sources[c.Source] = true

		sent := ms(m.SentAt)
		fresh := s.tri.IsFresh(m, now) && sent > lastRead && !m.AlreadyRead
		incUnread := b2i(fresh && !m.IsSelf)
		incMention := b2i(fresh && !m.IsSelf && m.MentionsMe)
		isNewest := sent >= lastAt
		// needs: -1 keep, 0 clear (you replied), 1 set
		needs := -1
		if isNewest && m.IsSelf {
			needs = 0
		} else if fresh && s.tri.NeedsAttention(c, m) {
			needs = 1
		}
		preview := m.Text
		if m.Subject != "" {
			preview = m.Subject
		}
		if r := []rune(preview); len(r) > 120 {
			preview = string(r[:120])
		}
		sender := m.SenderName
		if m.IsSelf {
			sender = "我"
		}
		_, err = tx.ExecContext(ctx, `
UPDATE conversations SET
  unread = unread + ?,
  mentions = mentions + ?,
  needs_attention = CASE ? WHEN -1 THEN needs_attention ELSE ? END,
  alert_hits = alert_hits + ?,
  msg_count = msg_count + 1,
  last_message_at = CASE WHEN ? THEN ? ELSE last_message_at END,
  last_preview = CASE WHEN ? THEN ? ELSE last_preview END,
  last_sender = CASE WHEN ? THEN ? ELSE last_sender END,
  change_seq = ?
WHERE id = ?`,
			incUnread, incMention, needs, needs, b2i(s.tri.MessageIsAlert(m)),
			isNewest, sent, isNewest, preview, isNewest, sender, s.nextSeq(), c.ID)
		if err != nil {
			return err
		}
		// A self message means you've seen everything before it.
		if m.IsSelf && isNewest {
			if _, err := tx.ExecContext(ctx, `UPDATE conversations SET unread = 0, mentions = 0, last_read_at = ? WHERE id = ?`, sent, c.ID); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for src := range sources {
		s.changed(src)
	}
	return nil
}

// LearnNames fills in sender_name for messages stored without one.
func (s *Store) LearnNames(ctx context.Context, source string, names map[string]string) error {
	if len(names) == 0 {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var n int64
	for id, name := range names {
		if id == "" || name == "" {
			continue
		}
		res, err := tx.ExecContext(ctx, `UPDATE messages SET sender_name = ? WHERE source = ? AND sender_id = ? AND sender_name = ''`, name, source, id)
		if err != nil {
			return err
		}
		k, _ := res.RowsAffected()
		n += k
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if n > 0 {
		s.changed(source)
	}
	return nil
}

// Cursor returns a stored sync cursor ("" if none).
func (s *Store) Cursor(ctx context.Context, source, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM cursors WHERE source = ? AND key = ?`, source, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetCursor persists a sync cursor.
func (s *Store) SetCursor(ctx context.Context, source, key, value string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO cursors (source, key, value) VALUES (?, ?, ?)
ON CONFLICT(source, key) DO UPDATE SET value = excluded.value`, source, key, value)
	return err
}

// ConversationRow is the API shape of a conversation.
type ConversationRow struct {
	model.Conversation
	LastMessageAt  time.Time `json:"last_message_at"`
	LastPreview    string    `json:"last_preview"`
	LastSender     string    `json:"last_sender"`
	Unread         int       `json:"unread"`
	Mentions       int       `json:"mentions"`
	NeedsAttention bool      `json:"needs_attention"`
	IsAlert        bool      `json:"is_alert"`
	CanSend        bool      `json:"can_send"`
}

// isAlert: a conversation is in the alert lane if its title matches an alert
// pattern, or if alert-bot messages dominate it (>= 3 hits and >= half of all
// messages) — one stray bot alert in a normal group is not enough.
const isAlert = "(title_alert = 1 OR (alert_hits >= 3 AND alert_hits * 2 >= msg_count))"

// Views map tab name -> WHERE clause. Alerts are excluded everywhere except
// their own lane.
var Views = map[string]string{
	"needs":   "needs_attention = 1 AND NOT " + isAlert,
	"all":     "NOT " + isAlert,
	"unread":  "unread > 0 AND NOT " + isAlert,
	"mention": "mentions > 0 AND NOT " + isAlert,
	"p2p":     "kind = 'p2p' AND NOT " + isAlert,
	"group":   "kind = 'group' AND NOT " + isAlert,
	"alert":   isAlert,
}

// ListConversations returns conversations for a view, newest first.
func (s *Store) ListConversations(ctx context.Context, view, source string, before time.Time, limit int) ([]ConversationRow, error) {
	where, ok := Views[view]
	if !ok {
		return nil, fmt.Errorf("unknown view %q", view)
	}
	conds := []string{where, "last_message_at > 0"}
	var args []any
	if source != "" {
		conds = append(conds, "source = ?")
		args = append(args, source)
	}
	if !before.IsZero() {
		conds = append(conds, "last_message_at < ?")
		args = append(args, ms(before))
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, source, platform, native_id, kind, title, avatar, last_message_at, last_preview, last_sender, unread, mentions, needs_attention, `+isAlert+`
FROM conversations WHERE `+strings.Join(conds, " AND ")+` ORDER BY last_message_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConvRows(rows)
}

func scanConvRows(rows *sql.Rows) ([]ConversationRow, error) {
	out := []ConversationRow{}
	for rows.Next() {
		var r ConversationRow
		var last int64
		var needs, alert int
		if err := rows.Scan(&r.ID, &r.Source, &r.Platform, &r.NativeID, &r.Kind, &r.Title, &r.Avatar, &last, &r.LastPreview, &r.LastSender, &r.Unread, &r.Mentions, &needs, &alert); err != nil {
			return nil, err
		}
		r.LastMessageAt, r.NeedsAttention, r.IsAlert = fromMS(last), needs == 1, alert == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// Counts returns the badge number for every view.
func (s *Store) Counts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for name, where := range Views {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversations WHERE last_message_at > 0 AND `+where).Scan(&n); err != nil {
			return nil, err
		}
		out[name] = n
	}
	return out, nil
}

// GetConversation looks up one conversation.
func (s *Store) GetConversation(ctx context.Context, id string) (model.Conversation, error) {
	var c model.Conversation
	err := s.db.QueryRowContext(ctx, `SELECT id, source, platform, native_id, kind, title, avatar FROM conversations WHERE id = ?`, id).
		Scan(&c.ID, &c.Source, &c.Platform, &c.NativeID, &c.Kind, &c.Title, &c.Avatar)
	return c, err
}

const msgCols = `m.id, m.conversation_id, m.sender_id, m.sender_name, m.is_self, m.type, m.text, m.subject, m.sent_at, m.mentions_me`

func scanMsg(sc interface{ Scan(...any) error }, extra ...any) (model.Message, error) {
	var m model.Message
	var self, mention int
	var sent int64
	dst := append([]any{&m.ID, &m.ConversationID, &m.SenderID, &m.SenderName, &self, &m.Type, &m.Text, &m.Subject, &sent, &mention}, extra...)
	if err := sc.Scan(dst...); err != nil {
		return m, err
	}
	m.IsSelf, m.MentionsMe, m.SentAt = self == 1, mention == 1, fromMS(sent)
	return m, nil
}

// ListMessages returns up to limit messages before `before`, oldest first.
func (s *Store) ListMessages(ctx context.Context, convID string, before time.Time, limit int) ([]model.Message, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	b := ms(before)
	if b == 0 {
		b = 1<<62 - 1
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+msgCols+` FROM messages m WHERE m.conversation_id = ? AND m.sent_at < ? ORDER BY m.sent_at DESC LIMIT ?`, convID, b, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Message{}
	for rows.Next() {
		m, err := scanMsg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// MarkRead marks a conversation read up to seenAt — the newest message the
// client had on screen (zero means "everything"). Messages that arrived
// after seenAt stay unread, so a stale client never swallows them.
func (s *Store) MarkRead(ctx context.Context, id string, seenAt time.Time) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := s.markReadTx(ctx, tx, id, seenAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.changed("")
	return nil
}

// markReadTx returns how many non-self messages remain unread after seenAt.
func (s *Store) markReadTx(ctx context.Context, tx *sql.Tx, id string, seenAt time.Time) (int, error) {
	var lastAt, lastRead int64
	err := tx.QueryRowContext(ctx, `SELECT last_message_at, last_read_at FROM conversations WHERE id = ?`, id).Scan(&lastAt, &lastRead)
	if err != nil {
		return 0, err
	}
	upTo := ms(seenAt)
	if upTo == 0 || upTo > lastAt {
		upTo = lastAt
	}
	newRead := max(lastRead, upTo)
	unread, mentions := 0, 0
	if newRead < lastAt {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), IFNULL(SUM(mentions_me), 0) FROM messages
WHERE conversation_id = ? AND is_self = 0 AND sent_at > ?`, id, newRead).Scan(&unread, &mentions); err != nil {
			return 0, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE conversations SET unread = ?, mentions = ?, last_read_at = ?, change_seq = ? WHERE id = ?`,
		unread, mentions, newRead, s.nextSeq(), id)
	return unread, err
}

// MarkDone removes a conversation from 需要看 and marks it read. If messages
// from others arrived after seenAt (the version the client acted on), the
// conversation is only marked read up to seenAt and ErrStale is returned —
// the newer messages must not be silently dismissed. Zero seenAt skips the
// check.
func (s *Store) MarkDone(ctx context.Context, id string, seenAt time.Time) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	remaining, err := s.markReadTx(ctx, tx, id, seenAt)
	if err != nil {
		return err
	}
	stale := !seenAt.IsZero() && remaining > 0
	if !stale {
		if _, err := tx.ExecContext(ctx, `UPDATE conversations SET needs_attention = 0 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.changed("")
	if stale {
		return ErrStale
	}
	return nil
}

// Changes returns conversations changed after `since`, oldest change first.
// reset=true means the client is too far behind (more than limit changes)
// and should refetch its views instead of applying a delta.
func (s *Store) Changes(ctx context.Context, since int64, limit int) (rows []ConversationRow, cur int64, reset bool, err error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	// Use the committed high-water mark, not the in-memory counter: a write
	// transaction may have taken seq numbers it has not committed yet.
	if cur, err = s.CommittedSeq(ctx); err != nil {
		return nil, 0, false, err
	}
	if since > cur { // hub DB was reset/replaced
		return nil, cur, true, nil
	}
	r, err := s.db.QueryContext(ctx, `
SELECT id, source, platform, native_id, kind, title, avatar, last_message_at, last_preview, last_sender, unread, mentions, needs_attention, `+isAlert+`
FROM conversations WHERE change_seq > ? AND change_seq <= ? ORDER BY change_seq ASC LIMIT ?`, since, cur, limit+1)
	if err != nil {
		return nil, cur, false, err
	}
	defer r.Close()
	rows, err = scanConvRows(r)
	if err != nil {
		return nil, cur, false, err
	}
	if len(rows) > limit {
		return nil, cur, true, nil
	}
	return rows, cur, false, nil
}

// Op is a stored idempotency receipt.
type Op struct {
	Status int // 0 = in progress
	Body   string
}

// BeginOp claims an idempotency key. If the key was seen before, it returns
// the stored receipt (Status 0 if the first request is still running).
func (s *Store) BeginOp(ctx context.Context, key string) (*Op, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO client_ops (key, created_at) VALUES (?, ?)`, key, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil, nil
	}
	var op Op
	err = s.db.QueryRowContext(ctx, `SELECT status, body FROM client_ops WHERE key = ?`, key).Scan(&op.Status, &op.Body)
	return &op, err
}

// FinishOp stores the final response for an idempotency key.
func (s *Store) FinishOp(ctx context.Context, key string, status int, body string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx, `UPDATE client_ops SET status = ?, body = ? WHERE key = ?`, status, body, key)
	return err
}

// SearchHit is a message plus its conversation context.
type SearchHit struct {
	model.Message
	ConversationTitle string `json:"conversation_title"`
	Platform          string `json:"platform"`
}

// Search does full-text search. Trigram FTS needs >= 3 characters; shorter
// queries (common for 2-char Chinese words) fall back to LIKE.
func (s *Store) Search(ctx context.Context, q string, limit int) ([]SearchHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []SearchHit{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if len([]rune(q)) >= 3 {
		phrase := `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
		rows, err = s.db.QueryContext(ctx, `
SELECT `+msgCols+`, c.title, c.platform FROM messages_fts f
JOIN messages m ON m.rowid = f.rowid JOIN conversations c ON c.id = m.conversation_id
WHERE messages_fts MATCH ? ORDER BY m.sent_at DESC LIMIT ?`, phrase, limit)
	} else {
		like := "%" + strings.NewReplacer("%", `\%`, "_", `\_`).Replace(q) + "%"
		rows, err = s.db.QueryContext(ctx, `
SELECT `+msgCols+`, c.title, c.platform FROM messages m JOIN conversations c ON c.id = m.conversation_id
WHERE m.text LIKE ? ESCAPE '\' OR m.subject LIKE ? ESCAPE '\' OR m.sender_name LIKE ? ESCAPE '\'
ORDER BY m.sent_at DESC LIMIT ?`, like, like, like, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchHit{}
	for rows.Next() {
		var h SearchHit
		m, err := scanMsg(rows, &h.ConversationTitle, &h.Platform)
		if err != nil {
			return nil, err
		}
		h.Message = m
		out = append(out, h)
	}
	return out, rows.Err()
}
