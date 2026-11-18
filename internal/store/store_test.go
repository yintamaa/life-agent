package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yintamaa/life-agent/internal/model"
	"github.com/yintamaa/life-agent/internal/triage"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	tri, err := triage.New(triage.Config{
		SelfNames: []string{"小黎"},
		Alert:     triage.AlertConfig{ConversationPatterns: []string{"告警"}, TextPatterns: []string{`\[FIRING\]`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), tri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func conv(id, kind, title string) model.Conversation {
	return model.Conversation{ID: "x:" + id, Source: "x", Platform: "feishu", NativeID: id, Kind: kind, Title: title}
}

func msg(id, conv string, self bool, text string, at time.Time) model.Message {
	return model.Message{ID: "x:" + id, ConversationID: "x:" + conv, Source: "x", SenderName: "张三", IsSelf: self, Type: "text", Text: text, SentAt: at}
}

func ids(rows []ConversationRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.NativeID)
	}
	return out
}

func TestViewsAndTriage(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	now := time.Now()
	for _, c := range []model.Conversation{
		conv("dm", "p2p", "张三"), conv("grp", "group", "项目群"), conv("alarm", "group", "线上告警群"),
		conv("old", "p2p", "老朋友"), conv("botgrp", "group", "普通群"),
	} {
		if err := s.UpsertConversation(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	botMsg := func(id, c string, at time.Time) model.Message {
		m := msg(id, c, false, "[FIRING] cpu high "+id, at)
		m.IsBot = true
		return m
	}
	err := s.IngestMessages(ctx, []model.Message{
		msg("1", "dm", false, "在吗", now.Add(-time.Minute)),
		msg("2", "grp", false, "大家好", now.Add(-2*time.Minute)),
		msg("3", "grp", false, "@小黎 帮忙看下", now.Add(-time.Minute)),
		msg("4", "alarm", false, "P0 故障", now.Add(-time.Second)),
		msg("6", "old", false, "很久以前", now.Add(-72*time.Hour)), // backfill: not fresh
		botMsg("5a", "botgrp", now.Add(-3*time.Second)),
		botMsg("5b", "botgrp", now.Add(-2*time.Second)),
		botMsg("5c", "botgrp", now),
		botMsg("5d", "grp", now.Add(-90*time.Second)), // one stray alert: grp stays normal
	})
	if err != nil {
		t.Fatal(err)
	}
	// duplicate ingest must be a no-op
	if err := s.IngestMessages(ctx, []model.Message{msg("1", "dm", false, "在吗", now.Add(-time.Minute))}); err != nil {
		t.Fatal(err)
	}
	check := func(view string, want ...string) {
		t.Helper()
		rows, err := s.ListConversations(ctx, view, "", time.Time{}, 0)
		if err != nil {
			t.Fatal(err)
		}
		got := ids(rows)
		if len(got) != len(want) {
			t.Fatalf("%s: got %v want %v", view, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v want %v", view, got, want)
			}
		}
	}
	check("needs", "dm", "grp")
	check("mention", "grp")
	check("alert", "botgrp", "alarm")
	check("unread", "dm", "grp")
	check("all", "dm", "grp", "old")
	rows, _ := s.ListConversations(ctx, "all", "", time.Time{}, 0)
	if rows[0].Unread != 1 || rows[1].Unread != 3 || rows[2].Unread != 0 {
		t.Fatalf("unread counts wrong: %+v", rows)
	}

	// Replying clears needs-attention and unread.
	if err := s.IngestMessages(ctx, []model.Message{msg("7", "dm", true, "在的", now)}); err != nil {
		t.Fatal(err)
	}
	check("needs", "grp")
	if err := s.MarkDone(ctx, "x:grp"); err != nil {
		t.Fatal(err)
	}
	check("needs")
	check("unread")

	c, err := s.Counts(ctx)
	if err != nil || c["alert"] != 2 || c["all"] != 3 {
		t.Fatalf("counts %v %v", c, err)
	}

	// Search: trigram (>=3 chars) and LIKE fallback (2 chars).
	for q, n := range map[string]int{"帮忙看": 1, "在吗": 1, "cpu high": 4, "不存在的词": 0} {
		hits, err := s.Search(ctx, q, 0)
		if err != nil || len(hits) != n {
			t.Fatalf("search %q: %d hits, err %v", q, len(hits), err)
		}
	}
	msgs, err := s.ListMessages(ctx, "x:dm", time.Time{}, 0)
	if err != nil || len(msgs) != 2 || msgs[0].Text != "在吗" || !msgs[1].IsSelf {
		t.Fatalf("messages %+v %v", msgs, err)
	}
}

func TestCursor(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if v, _ := s.Cursor(ctx, "a", "k"); v != "" {
		t.Fatal("expected empty")
	}
	_ = s.SetCursor(ctx, "a", "k", "1")
	_ = s.SetCursor(ctx, "a", "k", "2")
	if v, _ := s.Cursor(ctx, "a", "k"); v != "2" {
		t.Fatalf("got %q", v)
	}
}

func TestLearnNames(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	_ = s.UpsertConversation(ctx, conv("g", "group", "大群"))
	m := msg("1", "g", false, "hello world", time.Now())
	m.SenderID, m.SenderName = "ou_1", ""
	if err := s.IngestMessages(ctx, []model.Message{m}); err != nil {
		t.Fatal(err)
	}
	if err := s.LearnNames(ctx, "x", map[string]string{"ou_1": "李四"}); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.ListMessages(ctx, "x:g", time.Time{}, 0)
	if msgs[0].SenderName != "李四" {
		t.Fatalf("name not learned: %+v", msgs[0])
	}
	if hits, _ := s.Search(ctx, "李四", 0); len(hits) != 1 {
		t.Fatalf("search by learned name: %d", len(hits))
	}
	if hits, _ := s.Search(ctx, "hello world", 0); len(hits) != 1 {
		t.Fatalf("fts after update: %d", len(hits))
	}
}
