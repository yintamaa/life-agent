package wechat

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/yintamaa/life-agent/internal/connector"
	"github.com/yintamaa/life-agent/internal/model"
)

type memSink struct {
	mu      sync.Mutex
	convs   map[string]model.Conversation
	msgs    []model.Message
	cursors map[string]string
}

func (m *memSink) UpsertConversation(_ context.Context, c model.Conversation) error {
	m.convs[c.ID] = c
	return nil
}
func (m *memSink) IngestMessages(_ context.Context, ms []model.Message) error {
	m.msgs = append(m.msgs, ms...)
	return nil
}
func (m *memSink) LearnNames(context.Context, string, map[string]string) error { return nil }
func (m *memSink) Cursor(_ context.Context, s, k string) (string, error) { return m.cursors[s+k], nil }
func (m *memSink) SetCursor(_ context.Context, s, k, v string) error {
	m.cursors[s+k] = v
	return nil
}

var _ connector.Sink = (*memSink)(nil)

func mkdb(t *testing.T, path string, stmts ...string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func table(talker string) string {
	s := md5.Sum([]byte(talker))
	return "Msg_" + hex.EncodeToString(s[:])
}

func TestSyncDecryptedV4(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Unix()
	room := "123@chatroom"

	// group member display name via chat_room.ext_buffer protobuf
	var user []byte
	user = protowire.AppendTag(user, 1, protowire.BytesType)
	user = protowire.AppendString(user, "wxid_bob")
	user = protowire.AppendTag(user, 2, protowire.BytesType)
	user = protowire.AppendString(user, "鲍勃(群昵称)")
	var roomData []byte
	roomData = protowire.AppendTag(roomData, 1, protowire.BytesType)
	roomData = protowire.AppendBytes(roomData, user)

	mkdb(t, filepath.Join(dir, "session", "session.db"),
		`CREATE TABLE SessionTable(username TEXT, summary TEXT, last_timestamp INTEGER, last_msg_sender TEXT, last_sender_display_name TEXT)`,
		`INSERT INTO SessionTable VALUES ('wxid_alice','hi',`+itoa(now)+`,'',''), ('`+room+`','x',`+itoa(now)+`,'',''), ('gh_news','ad',`+itoa(now)+`,'','')`)
	cdb := filepath.Join(dir, "contact", "contact.db")
	mkdb(t, cdb,
		`CREATE TABLE contact(username TEXT, local_type INTEGER, alias TEXT, remark TEXT, nick_name TEXT)`,
		`INSERT INTO contact VALUES ('wxid_alice',1,'','爱丽丝',''), ('`+room+`',2,'','','家庭群')`,
		`CREATE TABLE chat_room(id INTEGER PRIMARY KEY, username TEXT, owner TEXT, ext_buffer BLOB)`)
	db, _ := sql.Open("sqlite", cdb)
	if _, err := db.Exec(`INSERT INTO chat_room(username, owner, ext_buffer) VALUES (?, '', ?)`, room, roomData); err != nil {
		t.Fatal(err)
	}
	db.Close()

	enc, _ := zstd.NewWriter(nil)
	compressed := enc.EncodeAll([]byte("wxid_bob:\n今晚吃什么 @小黎"), nil)
	mdb := filepath.Join(dir, "message", "message_0.db")
	schema := `(local_id INTEGER PRIMARY KEY AUTOINCREMENT, server_id INTEGER, local_type INTEGER, sort_seq INTEGER, real_sender_id INTEGER, create_time INTEGER, status INTEGER, message_content BLOB, packed_info_data BLOB)`
	mkdb(t, mdb,
		`CREATE TABLE Name2Id(user_name TEXT)`,
		`INSERT INTO Name2Id(rowid, user_name) VALUES (1,'wxid_alice'), (2,'wxid_me'), (3,'wxid_bob')`,
		`CREATE TABLE `+table("wxid_alice")+schema,
		`CREATE TABLE `+table(room)+schema)
	db, _ = sql.Open("sqlite", mdb)
	ins := func(talker string, seq, typ, sender int64, content any) {
		if _, err := db.Exec(`INSERT INTO `+table(talker)+`(server_id, local_type, sort_seq, real_sender_id, create_time, status, message_content) VALUES (0,?,?,?,?,0,?)`,
			typ, seq, sender, seq/1000, content); err != nil {
			t.Fatal(err)
		}
	}
	ins("wxid_alice", now*1000+1, 1, 1, "在吗")
	ins("wxid_alice", now*1000+2, 1, 2, "在的")
	ins("wxid_alice", now*1000+3, 49|(6<<32), 1, `<msg><appmsg><title>报价单.xlsx</title><type>6</type></appmsg></msg>`)
	ins(room, now*1000+4, 1, 3, compressed)
	db.Close()

	c := New(Config{DBDir: dir, SelfWxid: "wxid_me"})
	sink := &memSink{convs: map[string]model.Conversation{}, cursors: map[string]string{}}
	if err := c.sync(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.convs) != 2 {
		t.Fatalf("want 2 convs (gh_ skipped), got %v", sink.convs)
	}
	if sink.convs["wechat:"+room].Title != "家庭群" || sink.convs["wechat:"+room].Kind != model.KindGroup {
		t.Fatalf("room conv %+v", sink.convs["wechat:"+room])
	}
	if len(sink.msgs) != 4 {
		t.Fatalf("want 4 msgs got %d: %+v", len(sink.msgs), sink.msgs)
	}
	byText := map[string]model.Message{}
	for _, m := range sink.msgs {
		byText[m.Text] = m
	}
	if m := byText["在吗"]; m.IsSelf || m.SenderName != "爱丽丝" {
		t.Errorf("在吗: %+v", m)
	}
	if m := byText["在的"]; !m.IsSelf {
		t.Errorf("在的 should be self: %+v", m)
	}
	if m := byText["[文件] 报价单.xlsx"]; m.Type != model.TypeFile {
		t.Errorf("file msg missing: %+v", sink.msgs)
	}
	if m := byText["今晚吃什么 @小黎"]; m.SenderName != "鲍勃(群昵称)" || m.SenderID != "wxid_bob" {
		t.Errorf("group msg: %+v", m)
	}

	// Second round: nothing new.
	sink.msgs = nil
	if err := c.sync(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.msgs) != 0 {
		t.Fatalf("expected no new messages, got %+v", sink.msgs)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

