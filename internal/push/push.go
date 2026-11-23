// Package push delivers Web Push notifications (RFC 8030/8291, VAPID) to
// every subscribed device: the phone learns that an agent needs approval or
// finished, without keeping the page open. Payloads are end-to-end
// encrypted to the browser; the push service only sees ciphertext.
//
// iOS requires the page to be added to the Home Screen and served over
// HTTPS (tailscale serve gives both).
package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/yintamaa/life-agent/internal/store"
)

// Notification is what devices display.
type Notification struct {
	Title   string            `json:"title"`
	Body    string            `json:"body"`
	Tag     string            `json:"tag,omitempty"`  // same tag replaces the previous notification
	URL     string            `json:"url,omitempty"`  // opened on click (hash route)
	Kind    string            `json:"kind,omitempty"` // approval | task_done | task_failed | test
	Data    map[string]string `json:"data,omitempty"`
	Actions []Action          `json:"actions,omitempty"`
	Urgent  bool              `json:"-"`
}

// Action is a notification button (Chrome/Android/desktop; iOS ignores them).
type Action struct {
	Action string `json:"action"`
	Title  string `json:"title"`
}

type keys struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// Service sends notifications from a background queue.
type Service struct {
	st      *store.Store
	keys    keys
	subject string
	q       chan Notification
	client  webpush.HTTPClient
}

// New loads (or creates) VAPID keys under dataDir/vapid.json.
func New(st *store.Store, dataDir, subject string) (*Service, error) {
	if subject == "" {
		subject = "https://github.com/yintamaa/life-agent"
	}
	path := filepath.Join(dataDir, "vapid.json")
	var k keys
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &k); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, err
		}
		k = keys{Public: pub, Private: priv}
		b, _ := json.Marshal(k)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	return &Service{st: st, keys: k, subject: subject, q: make(chan Notification, 256), client: &http.Client{Timeout: 15 * time.Second}}, nil
}

// PublicKey is the VAPID application server key for PushManager.subscribe.
func (s *Service) PublicKey() string { return s.keys.Public }

// Notify queues a notification for all devices (never blocks).
func (s *Service) Notify(n Notification) {
	select {
	case s.q <- n:
	default:
		slog.Warn("push queue full, dropping", "title", n.Title)
	}
}

// Run delivers queued notifications until ctx is done.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-s.q:
			if _, err := s.Send(ctx, n); err != nil {
				slog.Warn("push", "err", err)
			}
		}
	}
}

// Send delivers n to every subscription now and returns how many succeeded.
// Subscriptions the push service reports gone (404/410) are removed.
func (s *Service) Send(ctx context.Context, n Notification) (int, error) {
	subs, err := s.st.ListPushSubs(ctx)
	if err != nil {
		return 0, err
	}
	payload, _ := json.Marshal(n)
	urgency := webpush.UrgencyNormal
	if n.Urgent {
		urgency = webpush.UrgencyHigh
	}
	ok := 0
	var errs []error
	for _, sub := range subs {
		resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
			Endpoint: sub.Endpoint, Keys: webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
		}, &webpush.Options{
			HTTPClient: s.client, Subscriber: s.subject, VAPIDPublicKey: s.keys.Public, VAPIDPrivateKey: s.keys.Private,
			TTL: 3600, Urgency: urgency, Topic: topic(n.Tag),
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			_ = s.st.DeletePushSub(ctx, sub.Endpoint) // device unsubscribed / expired
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			ok++
			s.st.TouchPushSub(ctx, sub.Endpoint)
		default:
			errs = append(errs, fmt.Errorf("push service %d for %s", resp.StatusCode, host(sub.Endpoint)))
		}
	}
	return ok, errors.Join(errs...)
}

// topic must be <= 32 chars from the URL-safe base64 alphabet.
func topic(tag string) string {
	out := make([]byte, 0, 32)
	for i := 0; i < len(tag) && len(out) < 32; i++ {
		c := tag[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}

func host(endpoint string) string {
	if len(endpoint) > 40 {
		return endpoint[:40] + "…"
	}
	return endpoint
}
