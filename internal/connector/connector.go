// Package connector defines the contract every IM adapter implements.
//
// A connector only moves data: it pulls (or receives) conversations and
// messages from one account on one platform, normalizes them into model
// types and hands them to a Sink. Everything else — storage, unread counts,
// triage, alert routing, search — lives in the hub.
package connector

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/yintamaa/life-agent/internal/model"
)

// Sink is what the hub exposes to connectors.
type Sink interface {
	// UpsertConversation creates or refreshes conversation metadata.
	UpsertConversation(ctx context.Context, c model.Conversation) error
	// IngestMessages stores messages (idempotent on Message.ID) and runs triage.
	IngestMessages(ctx context.Context, msgs []model.Message) error
	// Cursor / SetCursor persist per-connector sync positions.
	Cursor(ctx context.Context, source, key string) (string, error)
	SetCursor(ctx context.Context, source, key, value string) error
	// LearnNames back-fills sender names on already-stored messages whose
	// sender was unknown at ingest time (id -> display name).
	LearnNames(ctx context.Context, source string, names map[string]string) error
}

// Connector is one account on one platform.
type Connector interface {
	ID() string       // unique source id, e.g. "feishu", "mail:work"
	Platform() string // model.Platform*
	Name() string     // display name
	// Run blocks until ctx is cancelled. It should retry transient errors
	// itself and report health through st.
	Run(ctx context.Context, sink Sink, st *Status) error
}

// Sender is optionally implemented by connectors that can send text.
type Sender interface {
	Send(ctx context.Context, conversationNativeID, text string) error
}

// ErrSendUnsupported is returned when a source cannot send.
var ErrSendUnsupported = errors.New("该渠道暂不支持发送")

// Status is a concurrency-safe health record for one connector.
type Status struct {
	mu       sync.RWMutex
	s        model.SourceStatus
	onChange func()
}

// NewStatus creates a status in the connecting state.
func NewStatus(c Connector, onChange func()) *Status {
	return &Status{s: model.SourceStatus{
		ID: c.ID(), Platform: c.Platform(), Name: c.Name(), State: model.StateConnecting,
	}, onChange: onChange}
}

func (s *Status) set(f func(*model.SourceStatus)) {
	s.mu.Lock()
	f(&s.s)
	s.mu.Unlock()
	if s.onChange != nil {
		s.onChange()
	}
}

// Syncing marks a sync round as in progress (keeps last_sync_at).
func (s *Status) Syncing() { s.set(func(x *model.SourceStatus) { x.State = model.StateSyncing }) }

// OK marks a successful sync round.
func (s *Status) OK() {
	now := time.Now()
	s.set(func(x *model.SourceStatus) { x.State, x.Error, x.LastSyncAt = model.StateOK, "", &now })
}

// Fail records an error; the connector keeps retrying.
func (s *Status) Fail(err error) {
	s.set(func(x *model.SourceStatus) { x.State, x.Error = model.StateError, err.Error() })
}

// Connecting marks a (re)connect in progress.
func (s *Status) Connecting() { s.set(func(x *model.SourceStatus) { x.State = model.StateConnecting }) }

// Disabled marks a connector that will not run.
func (s *Status) Disabled(reason string) {
	s.set(func(x *model.SourceStatus) { x.State, x.Error = model.StateDisabled, reason })
}

// Snapshot returns a copy.
func (s *Status) Snapshot() model.SourceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.s
}

// PollLoop is a helper for polling connectors: it runs round immediately and
// then every interval, reporting status, with capped exponential backoff on
// consecutive failures.
func PollLoop(ctx context.Context, st *Status, interval time.Duration, round func(context.Context) error) error {
	backoff := interval
	for {
		st.Syncing()
		err := round(ctx)
		wait := interval
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			st.Fail(err)
			backoff = min(backoff*2, 10*time.Minute)
			wait = backoff
		} else {
			st.OK()
			backoff = interval
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}
