// Package model defines the platform-neutral data shapes every connector
// normalizes into. The hub, triage and API layers only ever see these types.
package model

import "time"

// Platform identifiers.
const (
	PlatformFeishu = "feishu"
	PlatformWeChat = "wechat"
	PlatformQQ     = "qq"
	PlatformMail   = "mail"
)

// Conversation kinds.
const (
	KindP2P   = "p2p"
	KindGroup = "group"
	KindMail  = "mail"
)

// Message types.
const (
	TypeText   = "text"
	TypeImage  = "image"
	TypeFile   = "file"
	TypeCard   = "card"
	TypeVoice  = "voice"
	TypeVideo  = "video"
	TypeLink   = "link"
	TypeSystem = "system"
	TypeMail   = "mail"
	TypeOther  = "other"
)

// Conversation is one chat / mail thread from one source.
// ID is globally unique: "<source>:<native id>".
type Conversation struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Platform string `json:"platform"`
	NativeID string `json:"-"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Avatar   string `json:"avatar"`
}

// Message is one message inside a conversation.
// ID is globally unique: "<source>:<native message id>".
type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Source         string    `json:"-"`
	SenderID       string    `json:"sender_id"`
	SenderName     string    `json:"sender_name"`
	IsSelf         bool      `json:"is_self"`
	Type           string    `json:"type"`
	Text           string    `json:"text"`
	Subject        string    `json:"subject"`
	SentAt         time.Time `json:"sent_at"`
	MentionsMe     bool      `json:"mentions_me"`
	// IsBot is a hint for triage (alert bots, notification accounts).
	IsBot bool `json:"-"`
	// AlreadyRead: the platform says you've seen it (e.g. IMAP \Seen), so it
	// must not raise unread / needs-attention.
	AlreadyRead bool `json:"-"`
}

// ConvID builds a conversation/message id from source + native id.
func ConvID(source, native string) string { return source + ":" + native }

// Source health states.
const (
	StateOK         = "ok"
	StateSyncing    = "syncing"
	StateConnecting = "connecting"
	StateError      = "error"
	StateDisabled   = "disabled"
)

// SourceStatus is what the status strip in the UI renders.
type SourceStatus struct {
	ID         string     `json:"id"`
	Platform   string     `json:"platform"`
	Name       string     `json:"name"`
	State      string     `json:"state"`
	LastSyncAt *time.Time `json:"last_sync_at"`
	Error      string     `json:"error"`
}
