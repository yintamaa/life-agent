// Package discord ingests Discord through a **bot** account: real-time
// messages over the Gateway plus history backfill over REST for every text
// channel / thread the bot can read in the servers it was added to, and DMs
// sent to the bot.
//
// It deliberately does NOT use a user token ("selfbot"): automating a user
// account violates Discord's Terms of Service and gets accounts banned. The
// consequence is that your personal DMs with friends are not visible; those
// can later be imported from Discord's official data package export.
//
// Bot setup (docs/connectors.md): create an application, enable the
// MESSAGE CONTENT privileged intent, invite it with View Channels + Read
// Message History (+ Send Messages if `send: true`).
package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/yintamaa/life-agent/internal/connector"
	"github.com/yintamaa/life-agent/internal/model"
)

// Config for the Discord connector.
type Config struct {
	Enabled  bool   `yaml:"enabled"`
	Name     string `yaml:"name"`
	Token    string `yaml:"token"`     // bot token (discouraged in plain text)
	TokenEnv string `yaml:"token_env"` // env var holding the bot token
	// SelfUserID is YOUR Discord user id (Developer Mode → Copy User ID):
	// your own messages count as "self", mentions of you as @我.
	SelfUserID string        `yaml:"self_user_id"`
	Guilds     []string      `yaml:"guilds"` // optional allowlist of server ids
	Backfill   time.Duration `yaml:"backfill"`
	Send       bool          `yaml:"send"`
}

// Connector implements connector.Connector and connector.Sender.
type Connector struct {
	cfg  Config
	sess *discordgo.Session

	mu          sync.Mutex
	cursors     map[string]string // channel id -> newest ingested message id
	initialized bool
	sink        connector.Sink
	st          *connector.Status
}

// New creates the connector.
func New(cfg Config) *Connector {
	if cfg.Name == "" {
		cfg.Name = "Discord"
	}
	if cfg.Backfill == 0 {
		cfg.Backfill = 72 * time.Hour
	}
	return &Connector{cfg: cfg, cursors: map[string]string{}}
}

func (c *Connector) ID() string       { return "discord" }
func (c *Connector) Platform() string { return model.PlatformDiscord }
func (c *Connector) Name() string     { return c.cfg.Name }

// CanSend reports whether sending is enabled.
func (c *Connector) CanSend() bool { return c.cfg.Send }

func (c *Connector) token() (string, error) {
	t := c.cfg.Token
	if c.cfg.TokenEnv != "" {
		t = os.Getenv(c.cfg.TokenEnv)
	}
	if t == "" {
		return "", errors.New("未配置 Discord bot token（discord.token_env）")
	}
	return strings.TrimPrefix(t, "Bot "), nil
}

// Run connects to the Gateway and keeps the session alive (discordgo
// reconnects by itself) until ctx is cancelled.
func (c *Connector) Run(ctx context.Context, sink connector.Sink, st *connector.Status) error {
	tok, err := c.token()
	if err != nil {
		st.Disabled(err.Error())
		<-ctx.Done()
		return ctx.Err()
	}
	s, err := discordgo.New("Bot " + tok)
	if err != nil {
		return err
	}
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent
	c.sess, c.sink, c.st = s, sink, st
	if v, _ := sink.Cursor(ctx, c.ID(), "init"); v != "" {
		c.initialized = true
	}

	s.AddHandler(func(_ *discordgo.Session, _ *discordgo.Disconnect) { st.Connecting() })
	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		go func() {
			st.Syncing()
			if err := c.backfill(ctx, r); err != nil {
				st.Fail(err)
				return
			}
			st.OK()
		}()
	})
	s.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if err := c.ingestLive(ctx, m.Message); err != nil {
			st.Fail(err)
		} else {
			st.OK()
		}
	})

	st.Connecting()
	if err := s.Open(); err != nil {
		return fmt.Errorf("连接 Discord 失败：%w（token 是否正确？是否开启了 MESSAGE CONTENT intent？）", err)
	}
	defer s.Close()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if s.DataReady {
				st.OK() // keeps "同步 N 秒前" fresh on quiet servers
			}
		}
	}
}

func (c *Connector) allowedGuild(id string) bool {
	if len(c.cfg.Guilds) == 0 || id == "" {
		return true
	}
	for _, g := range c.cfg.Guilds {
		if g == id {
			return true
		}
	}
	return false
}

func readable(ch *discordgo.Channel) bool {
	switch ch.Type {
	case discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildPublicThread, discordgo.ChannelTypeGuildPrivateThread, discordgo.ChannelTypeGuildNewsThread,
		discordgo.ChannelTypeDM, discordgo.ChannelTypeGroupDM:
		return true
	}
	return false
}

// snowflakeAt converts a time into a Discord snowflake (for "after" cursors).
func snowflakeAt(t time.Time) string {
	const discordEpoch = 1420070400000
	ms := t.UnixMilli() - discordEpoch
	if ms < 0 {
		ms = 0
	}
	return strconv.FormatInt(ms<<22, 10)
}

func snowLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// backfill fetches history for every readable channel since its cursor.
func (c *Connector) backfill(ctx context.Context, r *discordgo.Ready) error {
	var errs []error
	for _, g := range r.Guilds {
		if !c.allowedGuild(g.ID) {
			continue
		}
		chans, err := c.sess.GuildChannels(g.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("guild %s: %w", g.ID, err))
			continue
		}
		guildName := g.Name
		if gg, err := c.sess.State.Guild(g.ID); err == nil && gg.Name != "" {
			guildName = gg.Name
		}
		byID := map[string]*discordgo.Channel{}
		for _, ch := range chans {
			byID[ch.ID] = ch
		}
		for _, ch := range chans {
			if !readable(ch) {
				continue
			}
			if err := c.syncChannel(ctx, ch, guildName, byID[ch.ParentID]); err != nil {
				// Missing permissions on some channels is normal; keep going.
				slog.Debug("discord channel backfill", "channel", ch.Name, "err", err)
			}
		}
	}
	if len(errs) == 0 && !c.initialized {
		c.mu.Lock()
		c.initialized = true
		c.mu.Unlock()
		if err := c.sink.SetCursor(ctx, c.ID(), "init", "1"); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

func conversationFor(ch *discordgo.Channel, guildName string, parent *discordgo.Channel) model.Conversation {
	kind, title := model.KindGroup, "#"+ch.Name
	switch {
	case ch.Type == discordgo.ChannelTypeDM:
		kind = model.KindP2P
		if len(ch.Recipients) > 0 {
			title = ch.Recipients[0].DisplayName()
		}
	case ch.Type == discordgo.ChannelTypeGroupDM:
		var names []string
		for _, u := range ch.Recipients {
			names = append(names, u.DisplayName())
		}
		title = strings.Join(names, "、")
		if ch.Name != "" {
			title = ch.Name
		}
	case ch.IsThread() && parent != nil:
		title = "#" + parent.Name + " › " + ch.Name
	}
	if guildName != "" && kind == model.KindGroup && ch.Type != discordgo.ChannelTypeGroupDM {
		title += " · " + guildName
	}
	return model.Conversation{
		ID: model.ConvID("discord", ch.ID), Source: "discord", Platform: model.PlatformDiscord,
		NativeID: ch.ID, Kind: kind, Title: title,
	}
}

func (c *Connector) cursor(ctx context.Context, chID string) (string, error) {
	c.mu.Lock()
	cur, ok := c.cursors[chID]
	c.mu.Unlock()
	if ok {
		return cur, nil
	}
	return c.sink.Cursor(ctx, c.ID(), "ch:"+chID)
}

func (c *Connector) advance(ctx context.Context, chID, id string) error {
	c.mu.Lock()
	cur := c.cursors[chID]
	if cur != "" && !snowLess(cur, id) {
		c.mu.Unlock()
		return nil
	}
	c.cursors[chID] = id
	c.mu.Unlock()
	return c.sink.SetCursor(ctx, c.ID(), "ch:"+chID, id)
}

func (c *Connector) syncChannel(ctx context.Context, ch *discordgo.Channel, guildName string, parent *discordgo.Channel) error {
	conv := conversationFor(ch, guildName, parent)
	if err := c.sink.UpsertConversation(ctx, conv); err != nil {
		return err
	}
	after, err := c.cursor(ctx, ch.ID)
	if err != nil {
		return err
	}
	if after == "" {
		after = snowflakeAt(time.Now().Add(-c.cfg.Backfill))
	}
	for page := 0; page < 50; page++ { // up to 5000 messages per channel per round
		batch, err := c.sess.ChannelMessages(ch.ID, 100, "", after, "")
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		sort.Slice(batch, func(i, j int) bool { return snowLess(batch[i].ID, batch[j].ID) })
		msgs := make([]model.Message, 0, len(batch))
		for _, m := range batch {
			msg := c.normalize(conv, m)
			msg.AlreadyRead = !c.initialized
			msgs = append(msgs, msg)
		}
		if err := c.sink.IngestMessages(ctx, msgs); err != nil {
			return err
		}
		after = batch[len(batch)-1].ID
		if err := c.advance(ctx, ch.ID, after); err != nil {
			return err
		}
		if len(batch) < 100 {
			return nil
		}
	}
	return nil
}

func (c *Connector) ingestLive(ctx context.Context, m *discordgo.Message) error {
	if m == nil || !c.allowedGuild(m.GuildID) {
		return nil
	}
	ch, err := c.sess.State.Channel(m.ChannelID)
	if err != nil {
		if ch, err = c.sess.Channel(m.ChannelID); err != nil {
			return err
		}
	}
	guildName := ""
	var parent *discordgo.Channel
	if m.GuildID != "" {
		if g, err := c.sess.State.Guild(m.GuildID); err == nil {
			guildName = g.Name
		}
		if ch.ParentID != "" {
			parent, _ = c.sess.State.Channel(ch.ParentID)
		}
	}
	conv := conversationFor(ch, guildName, parent)
	if err := c.sink.UpsertConversation(ctx, conv); err != nil {
		return err
	}
	if err := c.sink.IngestMessages(ctx, []model.Message{c.normalize(conv, m)}); err != nil {
		return err
	}
	return c.advance(ctx, ch.ID, m.ID)
}

func (c *Connector) botID() string {
	if c.sess != nil && c.sess.State != nil && c.sess.State.User != nil {
		return c.sess.State.User.ID
	}
	return ""
}

func (c *Connector) normalize(conv model.Conversation, m *discordgo.Message) model.Message {
	msg := model.Message{
		ID:             model.ConvID(c.ID(), m.ID),
		ConversationID: conv.ID,
		Source:         c.ID(),
		SentAt:         m.Timestamp,
		Type:           model.TypeText,
	}
	if m.Author != nil {
		msg.SenderID = m.Author.ID
		msg.SenderName = m.Author.DisplayName()
		msg.IsBot = m.Author.Bot
		// Your own messages, and what the bot sent on your behalf, are "self".
		msg.IsSelf = (c.cfg.SelfUserID != "" && m.Author.ID == c.cfg.SelfUserID) || (m.Author.ID != "" && m.Author.ID == c.botID())
		if msg.IsSelf {
			msg.IsBot = false
		}
	}
	if m.Member != nil && m.Member.Nick != "" {
		msg.SenderName = m.Member.Nick
	}
	for _, u := range m.Mentions {
		if u != nil && c.cfg.SelfUserID != "" && u.ID == c.cfg.SelfUserID {
			msg.MentionsMe = true
		}
	}
	parts := []string{strings.TrimSpace(m.ContentWithMentionsReplaced())}
	for _, a := range m.Attachments {
		if a != nil {
			parts = append(parts, "[附件] "+a.Filename)
		}
	}
	for _, e := range m.Embeds {
		if e != nil && (e.Title != "" || e.Description != "") {
			parts = append(parts, strings.TrimSpace("[卡片] "+e.Title+" "+e.Description))
		}
	}
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	msg.Text = strings.Join(nonEmpty, "\n")
	switch {
	case m.Type != discordgo.MessageTypeDefault && m.Type != discordgo.MessageTypeReply:
		msg.Type = model.TypeSystem
		if msg.Text == "" {
			msg.Text = "[系统消息]"
		}
	case len(m.Content) == 0 && len(m.Attachments) > 0:
		msg.Type = model.TypeFile
	case len(m.Content) == 0 && len(m.Embeds) > 0:
		msg.Type = model.TypeCard
	}
	return msg
}

// Send posts a message to a channel (disabled unless cfg.Send).
func (c *Connector) Send(_ context.Context, channelID, text string) error {
	if !c.cfg.Send {
		return connector.ErrSendUnsupported
	}
	if c.sess == nil {
		return errors.New("Discord 未连接")
	}
	_, err := c.sess.ChannelMessageSend(channelID, text)
	return err
}
