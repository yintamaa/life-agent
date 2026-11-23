package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yintamaa/life-agent/internal/store"
	"github.com/yintamaa/life-agent/internal/triage"
)

func browserKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, 16)
	_, _ = rand.Read(a)
	enc := base64.RawURLEncoding
	return enc.EncodeToString(k.PublicKey().Bytes()), enc.EncodeToString(a)
}

func TestSendEncryptsAndPrunes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tri, _ := triage.New(triage.Config{})
	st, err := store.Open(filepath.Join(dir, "t.db"), tri)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var mu sync.Mutex
	var got []*http.Request
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Clone(context.Background()))
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/gone") {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	s, err := New(st, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	s.client = ts.Client()
	// Keys persist across restarts.
	s2, _ := New(st, dir, "")
	if s2.PublicKey() != s.PublicKey() || s.PublicKey() == "" {
		t.Fatal("VAPID keys must be generated once and reloaded")
	}
	for _, path := range []string{"/phone", "/gone"} {
		p, a := browserKeys(t)
		if err := st.SavePushSub(ctx, store.PushSub{Endpoint: ts.URL + path, P256dh: p, Auth: a}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Send(ctx, Notification{Title: "需要审批", Body: "git push", Tag: "approval-abc", Urgent: true})
	if err != nil || n != 1 {
		t.Fatalf("delivered %d, err %v", n, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("expected 2 push requests, got %d", len(got))
	}
	r := got[0]
	if !strings.HasPrefix(r.Header.Get("Authorization"), "vapid t=") || r.Header.Get("Content-Encoding") != "aes128gcm" ||
		r.Header.Get("Urgency") != "high" || r.Header.Get("Topic") != "approval-abc" || r.Header.Get("TTL") == "" {
		t.Fatalf("push headers: %v", r.Header)
	}
	subs, _ := st.ListPushSubs(ctx)
	if len(subs) != 1 || !strings.HasSuffix(subs[0].Endpoint, "/phone") {
		t.Fatalf("410 subscription must be pruned: %+v", subs)
	}
}

func TestTopic(t *testing.T) {
	if got := topic("approval-1a2b:c/d"); got != "approval-1a2bcd" {
		t.Fatalf("topic %q", got)
	}
	if len(topic(strings.Repeat("x", 50))) != 32 {
		t.Fatal("topic must be <= 32 chars")
	}
}
