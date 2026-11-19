// Package triage decides, per incoming message, whether it needs the user's
// attention and whether its conversation belongs in the alert lane.
//
// Rules are deliberately simple and deterministic for now; an LLM scorer can
// later be layered on top of NeedsAttention without changing the store.
package triage

import (
	"regexp"
	"strings"
	"time"

	"github.com/yintamaa/life-agent/internal/model"
)

// Config is the user-tunable part (see config.example.yaml).
type Config struct {
	// FreshWindow: messages older than this at ingest time (history backfill)
	// never raise unread / needs-attention.
	FreshWindow time.Duration `yaml:"fresh_window"`
	// SelfNames are your display names, used to detect "@你" in plain text
	// for platforms that don't give structured mentions (WeChat).
	SelfNames []string `yaml:"self_names"`
	// VIP senders (display name substring) always need attention.
	VIP []string `yaml:"vip"`
	// Conversations whose title matches never need attention (still unread).
	MutePatterns []string `yaml:"mute_patterns"`
	// MailNeedsAttention: whether ordinary new mail lands in 需要看.
	MailNeedsAttention bool `yaml:"mail_needs_attention"`
	// MentionAll: whether @所有人 counts as @我 (noisy in big groups; default off).
	MentionAll bool        `yaml:"mention_all"`
	Alert      AlertConfig `yaml:"alert"`
}

// AlertConfig routes conversations to the 线上告警 lane.
type AlertConfig struct {
	ConversationPatterns []string `yaml:"conversation_patterns"`
	SenderPatterns       []string `yaml:"sender_patterns"`
	// TextPatterns only apply to bot senders, so humans quoting "P0" in a
	// normal group don't turn it into an alert channel.
	TextPatterns []string `yaml:"text_patterns"`
}

// Engine is the compiled rule set.
type Engine struct {
	cfg                          Config
	mute, alertConv, alertSender []*regexp.Regexp
	alertText                    []*regexp.Regexp
}

// New compiles the rules. Patterns are case-insensitive regexps.
func New(cfg Config) (*Engine, error) {
	if cfg.FreshWindow == 0 {
		cfg.FreshWindow = 24 * time.Hour
	}
	e := &Engine{cfg: cfg}
	var err error
	for _, p := range []struct {
		dst *[]*regexp.Regexp
		src []string
	}{
		{&e.mute, cfg.MutePatterns},
		{&e.alertConv, cfg.Alert.ConversationPatterns},
		{&e.alertSender, cfg.Alert.SenderPatterns},
		{&e.alertText, cfg.Alert.TextPatterns},
	} {
		if *p.dst, err = compile(p.src); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func compile(ps []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(ps))
	for _, p := range ps {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// IsFresh reports whether a message is recent enough to count as new.
func (e *Engine) IsFresh(m model.Message, now time.Time) bool {
	return now.Sub(m.SentAt) <= e.cfg.FreshWindow
}

// ConversationIsAlert decides from conversation metadata alone.
func (e *Engine) ConversationIsAlert(c model.Conversation) bool {
	return anyMatch(e.alertConv, c.Title)
}

// MessageIsAlert decides from a message (sender / bot text).
func (e *Engine) MessageIsAlert(m model.Message) bool {
	if anyMatch(e.alertSender, m.SenderName) {
		return true
	}
	return m.IsBot && anyMatch(e.alertText, m.Text)
}

// DetectMention fills MentionsMe from plain text when the connector could not.
func (e *Engine) DetectMention(m *model.Message) {
	if m.MentionsMe || m.IsSelf {
		return
	}
	for _, n := range e.cfg.SelfNames {
		if n != "" && strings.Contains(m.Text, "@"+n) {
			m.MentionsMe = true
			return
		}
	}
	if e.cfg.MentionAll && (strings.Contains(m.Text, "@所有人") || strings.Contains(m.Text, "@all")) {
		m.MentionsMe = true
	}
}

// MentionAll reports whether @所有人 should count as a mention.
func (e *Engine) MentionAll() bool { return e.cfg.MentionAll }

// NeedsAttention: does this (fresh, not-self) message put its conversation in 需要看?
func (e *Engine) NeedsAttention(c model.Conversation, m model.Message) bool {
	if m.IsSelf || anyMatch(e.mute, c.Title) {
		return false
	}
	for _, v := range e.cfg.VIP {
		if v != "" && strings.Contains(m.SenderName, v) {
			return true
		}
	}
	if m.MentionsMe {
		return true
	}
	switch c.Kind {
	case model.KindP2P:
		return !m.IsBot
	case model.KindMail:
		return e.cfg.MailNeedsAttention
	}
	return false
}
