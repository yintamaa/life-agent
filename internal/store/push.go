package store

import (
	"context"
	"time"
)

const pushSchema = `
CREATE TABLE IF NOT EXISTS push_subscriptions (
  endpoint TEXT PRIMARY KEY,
  p256dh TEXT NOT NULL,
  auth TEXT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  last_ok_at INTEGER NOT NULL DEFAULT 0
);
`

// PushSub is one browser push subscription (one per device/browser).
type PushSub struct {
	Endpoint  string    `json:"endpoint"`
	P256dh    string    `json:"p256dh"`
	Auth      string    `json:"auth"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) migratePush() error {
	_, err := s.db.Exec(pushSchema)
	return err
}

// SavePushSub upserts a subscription.
func (s *Store) SavePushSub(ctx context.Context, p PushSub) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_agent, created_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth, user_agent = excluded.user_agent`,
		p.Endpoint, p.P256dh, p.Auth, p.UserAgent, time.Now().UnixMilli())
	return err
}

// DeletePushSub removes a subscription (unsubscribed or expired).
func (s *Store) DeletePushSub(ctx context.Context, endpoint string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// TouchPushSub records a successful delivery.
func (s *Store) TouchPushSub(ctx context.Context, endpoint string) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.db.ExecContext(ctx, `UPDATE push_subscriptions SET last_ok_at = ? WHERE endpoint = ?`, time.Now().UnixMilli(), endpoint)
}

// ListPushSubs returns all subscriptions.
func (s *Store) ListPushSubs(ctx context.Context) ([]PushSub, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT endpoint, p256dh, auth, user_agent, created_at FROM push_subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PushSub{}
	for rows.Next() {
		var p PushSub
		var created int64
		if err := rows.Scan(&p.Endpoint, &p.P256dh, &p.Auth, &p.UserAgent, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = fromMS(created)
		out = append(out, p)
	}
	return out, rows.Err()
}
