package api

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yintamaa/life-agent/internal/connector"
	"github.com/yintamaa/life-agent/internal/model"
	"github.com/yintamaa/life-agent/internal/store"
	"github.com/yintamaa/life-agent/internal/triage"
)

type fakeConn struct {
	sends atomic.Int32
	fail  bool
}

func (f *fakeConn) ID() string       { return "x" }
func (f *fakeConn) Platform() string { return "feishu" }
func (f *fakeConn) Name() string     { return "X" }
func (f *fakeConn) Run(ctx context.Context, _ connector.Sink, _ *connector.Status) error {
	<-ctx.Done()
	return nil
}
func (f *fakeConn) Send(context.Context, string, string) error {
	f.sends.Add(1)
	if f.fail {
		return errors.New("boom")
	}
	return nil
}

func setup(t *testing.T) (*Server, *store.Store, *fakeConn) {
	t.Helper()
	tri, _ := triage.New(triage.Config{})
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), tri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fc := &fakeConn{}
	srv := NewServer(st, map[string]connector.Connector{"x": fc}, map[string]*connector.Status{}, nil)
	st.OnChange = srv.Notify
	ctx := context.Background()
	_ = st.UpsertConversation(ctx, model.Conversation{ID: "x:c", Source: "x", Platform: "feishu", NativeID: "c", Kind: "p2p", Title: "C"})
	_ = st.IngestMessages(ctx, []model.Message{{ID: "x:1", ConversationID: "x:c", Source: "x", Text: "hi", Type: "text", SentAt: time.Now()}})
	return srv, st, fc
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:5555"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestIdempotentSend(t *testing.T) {
	srv, _, fc := setup(t)
	h := srv.Handler()
	hdr := map[string]string{"Idempotency-Key": "abc"}
	for i := 0; i < 3; i++ {
		if w := do(h, "POST", "/api/conversations/x:c/send", `{"text":"hello"}`, hdr); w.Code != 204 {
			t.Fatalf("send %d: %d %s", i, w.Code, w.Body)
		}
	}
	if n := fc.sends.Load(); n != 1 {
		t.Fatalf("connector called %d times, want 1", n)
	}
	// Failures are replayed too (a user-initiated retry uses a new key).
	fc.fail = true
	hdr = map[string]string{"Idempotency-Key": "def"}
	w1 := do(h, "POST", "/api/conversations/x:c/send", `{"text":"x"}`, hdr)
	w2 := do(h, "POST", "/api/conversations/x:c/send", `{"text":"x"}`, hdr)
	if w1.Code != 502 || w2.Code != 502 || w2.Header().Get("Idempotent-Replayed") != "true" || fc.sends.Load() != 2 {
		t.Fatalf("failure replay: %d %d sends=%d", w1.Code, w2.Code, fc.sends.Load())
	}
}

func TestDoneConflict(t *testing.T) {
	srv, _, _ := setup(t)
	h := srv.Handler()
	old := time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	if w := do(h, "POST", "/api/conversations/x:c/done", `{"seen_at":"`+old+`"}`, nil); w.Code != 409 {
		t.Fatalf("want 409, got %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/conversations/x:c/done", "", nil); w.Code != 204 {
		t.Fatalf("want 204, got %d", w.Code)
	}
	if w := do(h, "POST", "/api/conversations/x:nope/done", "", nil); w.Code != 404 {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestSSEResume(t *testing.T) {
	srv, st, _ := setup(t)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close) // registered first => runs after the SSE bodies are closed
	cur, _ := st.CommittedSeq(context.Background())

	open := func(last string) *bufio.Reader {
		req, _ := http.NewRequest("GET", ts.URL+"/api/events", nil)
		if last != "" {
			req.Header.Set("Last-Event-ID", last)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return bufio.NewReader(resp.Body)
	}
	readEvent := func(br *bufio.Reader) string {
		done := make(chan string, 1)
		go func() {
			var b strings.Builder
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					done <- ""
					return
				}
				b.WriteString(line)
				if strings.HasPrefix(line, "data: ") {
					done <- b.String()
					return
				}
			}
		}()
		select {
		case s := <-done:
			return s
		case <-time.After(3 * time.Second):
			return ""
		}
	}

	// A client that was behind gets an immediate catch-up event.
	if ev := readEvent(open("0")); !strings.Contains(ev, "event: changed") {
		t.Fatalf("expected catch-up event, got %q", ev)
	}
	// An up-to-date client gets the next live change with a new id.
	br := open(itoa(cur))
	time.Sleep(100 * time.Millisecond)
	_ = st.IngestMessages(context.Background(), []model.Message{{ID: "x:2", ConversationID: "x:c", Source: "x", Text: "yo", Type: "text", SentAt: time.Now()}})
	ev := readEvent(br)
	if !strings.Contains(ev, "id: "+itoa(cur+1)+"\nevent: changed") {
		t.Fatalf("expected live event with newer id, got %q", ev)
	}
}

func itoa(n int64) string { return formatInt(n) }

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestAuthPolicy(t *testing.T) {
	cfg := AuthConfig{Tokens: []string{"s3cret"}}
	cfg.Tailscale.Enabled = true
	cfg.Tailscale.AllowedLogins = []string{"me@github"}
	a := NewAuthenticator(cfg)
	a.whois = func(_ context.Context, ip string) (whoisResult, error) {
		switch ip {
		case "100.64.0.1":
			return whoisResult{Login: "me@github"}, nil
		case "100.64.0.2":
			return whoisResult{Login: "other@github"}, nil
		case "100.64.0.3":
			return whoisResult{Login: "tagged-devices", Tagged: true}, nil
		}
		return whoisResult{}, errors.New("unknown")
	}
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(UserFrom(r.Context()))) }))
	cases := []struct {
		name, remote string
		hdr          map[string]string
		code         int
		user         string
	}{
		{"local direct", "127.0.0.1:1", nil, 200, "local"},
		{"serve, allowed login", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "100.64.0.1", "Tailscale-User-Login": "me@github"}, 200, "me@github"},
		{"serve, other login", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "100.64.0.2"}, 403, ""},
		{"serve, forged header", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "100.64.0.2", "Tailscale-User-Login": "me@github"}, 403, ""},
		{"serve, tagged device", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "100.64.0.3"}, 403, ""},
		{"lan peer, no token", "192.168.1.5:1", nil, 401, ""},
		{"lan peer forging xff", "192.168.1.5:1", map[string]string{"X-Forwarded-For": "100.64.0.1"}, 401, ""},
		{"lan peer, token", "192.168.1.5:1", map[string]string{"Authorization": "Bearer s3cret"}, 200, "token"},
		{"bad token", "192.168.1.5:1", map[string]string{"Authorization": "Bearer nope"}, 401, ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/api/counts", nil)
		r.RemoteAddr = c.remote
		for k, v := range c.hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.code || (c.code == 200 && w.Body.String() != c.user) {
			t.Errorf("%s: got %d %q, want %d %q", c.name, w.Code, w.Body.String(), c.code, c.user)
		}
	}
}
