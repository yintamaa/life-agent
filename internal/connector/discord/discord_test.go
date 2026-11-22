package discord

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/yintamaa/life-agent/internal/model"
)

func TestConversationFor(t *testing.T) {
	parent := &discordgo.Channel{ID: "10", Name: "general", Type: discordgo.ChannelTypeGuildText}
	cases := []struct {
		ch         *discordgo.Channel
		guild      string
		parent     *discordgo.Channel
		kind, want string
	}{
		{parent, "家庭服务器", nil, model.KindGroup, "#general · 家庭服务器"},
		{&discordgo.Channel{ID: "11", Name: "周末计划", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "10"}, "家庭服务器", parent, model.KindGroup, "#general › 周末计划 · 家庭服务器"},
		{&discordgo.Channel{ID: "12", Type: discordgo.ChannelTypeDM, Recipients: []*discordgo.User{{ID: "u1", Username: "alice", GlobalName: "Alice"}}}, "", nil, model.KindP2P, "Alice"},
	}
	for _, c := range cases {
		conv := conversationFor(c.ch, c.guild, c.parent)
		if conv.Kind != c.kind || conv.Title != c.want || conv.ID != "discord:"+c.ch.ID {
			t.Errorf("got %+v, want kind=%s title=%s", conv, c.kind, c.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	c := New(Config{SelfUserID: "me"})
	conv := model.Conversation{ID: "discord:10", NativeID: "10", Kind: model.KindGroup}
	ts := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	m := &discordgo.Message{
		ID: "1001", ChannelID: "10", Timestamp: ts, Type: discordgo.MessageTypeDefault,
		Content:     "<@me> 周末去爬山吗",
		Author:      &discordgo.User{ID: "u2", Username: "bob", GlobalName: "Bob"},
		Member:      &discordgo.Member{Nick: "鲍勃"},
		Mentions:    []*discordgo.User{{ID: "me", Username: "yin"}},
		Attachments: []*discordgo.MessageAttachment{{Filename: "route.gpx"}},
	}
	msg := c.normalize(conv, m)
	if msg.ID != "discord:1001" || msg.SenderName != "鲍勃" || !msg.MentionsMe || msg.IsSelf || msg.IsBot {
		t.Fatalf("normalize: %+v", msg)
	}
	if msg.Text != "@yin 周末去爬山吗\n[附件] route.gpx" || !msg.SentAt.Equal(ts) {
		t.Fatalf("text %q", msg.Text)
	}
	own := c.normalize(conv, &discordgo.Message{ID: "1002", Content: "好", Author: &discordgo.User{ID: "me"}, Type: discordgo.MessageTypeDefault})
	if !own.IsSelf {
		t.Fatal("own message must be self")
	}
	bot := c.normalize(conv, &discordgo.Message{ID: "1003", Author: &discordgo.User{ID: "b", Bot: true}, Type: discordgo.MessageTypeDefault,
		Embeds: []*discordgo.MessageEmbed{{Title: "[FIRING] disk full", Description: "nas-01"}}})
	if !bot.IsBot || bot.Type != model.TypeCard || bot.Text != "[卡片] [FIRING] disk full nas-01" {
		t.Fatalf("bot embed: %+v", bot)
	}
	join := c.normalize(conv, &discordgo.Message{ID: "1004", Author: &discordgo.User{ID: "u3"}, Type: discordgo.MessageTypeGuildMemberJoin})
	if join.Type != model.TypeSystem {
		t.Fatalf("join: %+v", join)
	}
}

func TestSnowflake(t *testing.T) {
	// Discord epoch + 1s → 1000<<22
	if got := snowflakeAt(time.UnixMilli(1420070401000)); got != "4194304000" {
		t.Fatalf("snowflake %s", got)
	}
	if !snowLess("999", "1000") || snowLess("1000", "999") || !snowLess("1001", "1002") {
		t.Fatal("snowLess ordering")
	}
}
