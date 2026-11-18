// Package api serves the JSON API, the SSE change feed and the embedded SPA.
//
// Multi-device consistency model (see docs/competitive-analysis.md §4):
//   - The hub is the single source of truth; devices are views.
//   - Every visible change stamps the conversation with a hub-wide change_seq.
//     SSE events carry it as their id, so a reconnecting EventSource sends
//     Last-Event-ID and is told immediately if it missed anything;
//     GET /api/changes?since=N returns the delta (or reset=true).
//   - read/done take the version the client saw (seen_at); newer messages
//     are never swallowed by a stale device.
//   - send takes an Idempotency-Key; retries replay the first result.
//   - A subscriber that can't keep up is disconnected rather than silently
//     dropped, so it reconnects and catches up.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yintamaa/life-agent/internal/connector"
	"github.com/yintamaa/life-agent/internal/model"
	"github.com/yintamaa/life-agent/internal/store"
)

// Server wires HTTP handlers to the store and connectors.
type Server struct {
	Store      *store.Store
	Connectors map[string]connector.Connector
	Statuses   map[string]*connector.Status
	Static     fs.FS // web/dist; may be nil
	Auth       *Authenticator

	mu   sync.Mutex
	subs map[*subscriber]struct{}
	kick chan string
}

type subscriber struct {
	ch   chan event
	gone chan struct{} // closed when the hub drops a slow subscriber
}

type event struct {
	Seq    int64  `json:"seq"`
	Source string `json:"source"`
}

// NewServer creates a server; call Notify on any data/status change.
func NewServer(st *store.Store, conns map[string]connector.Connector, stats map[string]*connector.Status, static fs.FS) *Server {
	s := &Server{Store: st, Connectors: conns, Statuses: stats, Static: static,
		subs: map[*subscriber]struct{}{}, kick: make(chan string, 256)}
	go s.fanout()
	return s
}

// Notify schedules a "changed" SSE event (coalesced to <= 2/s).
func (s *Server) Notify(source string) {
	select {
	case s.kick <- source:
	default: // a flush is already pending; it will carry the latest seq
	}
}

func (s *Server) fanout() {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	pending := map[string]bool{}
	for {
		select {
		case src := <-s.kick:
			pending[src] = true
		case <-t.C:
			if len(pending) == 0 {
				continue
			}
			seq, err := s.Store.CommittedSeq(context.Background())
			if err != nil {
				slog.Warn("sse: read seq", "err", err)
				continue
			}
			s.mu.Lock()
			for src := range pending {
				for sub := range s.subs {
					select {
					case sub.ch <- event{Seq: seq, Source: src}:
					default:
						// Never drop silently: disconnect, the client resumes
						// with Last-Event-ID and refetches.
						delete(s.subs, sub)
						close(sub.gone)
					}
				}
			}
			s.mu.Unlock()
			pending = map[string]bool{}
		}
	}
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sources", s.sources)
	mux.HandleFunc("GET /api/counts", s.counts)
	mux.HandleFunc("GET /api/conversations", s.conversations)
	mux.HandleFunc("GET /api/conversations/{id}/messages", s.messages)
	mux.HandleFunc("POST /api/conversations/{id}/read", s.read)
	mux.HandleFunc("POST /api/conversations/{id}/done", s.done)
	mux.HandleFunc("POST /api/conversations/{id}/send", s.send)
	mux.HandleFunc("GET /api/changes", s.changes)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/whoami", s.whoami)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusNotFound, "not found") })
	if s.Static != nil {
		mux.Handle("/", spa(s.Static))
	}
	var h http.Handler = mux
	if s.Auth != nil {
		h = s.Auth.Middleware(h)
	}
	return logRequests(h)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func (s *Server) sources(w http.ResponseWriter, _ *http.Request) {
	out := make([]model.SourceStatus, 0, len(s.Statuses))
	for _, st := range s.Statuses {
		out = append(out, st.Snapshot())
	}
	order := map[string]int{model.PlatformWeChat: 0, model.PlatformFeishu: 1, model.PlatformQQ: 2, model.PlatformMail: 3}
	sort.Slice(out, func(i, j int) bool {
		if order[out[i].Platform] != order[out[j].Platform] {
			return order[out[i].Platform] < order[out[j].Platform]
		}
		return out[i].ID < out[j].ID
	})
	writeJSON(w, out)
}

func (s *Server) counts(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.Counts(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, c)
}

func (s *Server) conversations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	view := q.Get("view")
	if view == "" {
		view = "all"
	}
	rows, err := s.Store.ListConversations(r.Context(), view, q.Get("source"), parseTime(q.Get("before")), atoi(q.Get("limit")))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s.fillCanSend(rows)
	writeJSON(w, rows)
}

func (s *Server) fillCanSend(rows []store.ConversationRow) {
	for i := range rows {
		rows[i].CanSend = s.canSend(rows[i].Source)
	}
}

func (s *Server) canSend(source string) bool {
	c, ok := s.Connectors[source]
	if !ok {
		return false
	}
	type gate interface{ CanSend() bool }
	if g, ok := c.(gate); ok {
		return g.CanSend()
	}
	_, ok = c.(connector.Sender)
	return ok
}

func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	rows, cur, reset, err := s.Store.Changes(r.Context(), since, atoi(q.Get("limit")))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.fillCanSend(rows)
	if rows == nil {
		rows = []store.ConversationRow{}
	}
	writeJSON(w, map[string]any{"seq": cur, "reset": reset, "conversations": rows})
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	msgs, err := s.Store.ListMessages(r.Context(), r.PathValue("id"), parseTime(q.Get("before")), atoi(q.Get("limit")))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, msgs)
}

// seenAt reads the optional {"seen_at": RFC3339} body.
func seenAt(r *http.Request) time.Time {
	var body struct {
		SeenAt string `json:"seen_at"`
	}
	if r.ContentLength != 0 {
		_ = json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<10)).Decode(&body)
	}
	return parseTime(body.SeenAt)
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	err := s.Store.MarkRead(r.Context(), r.PathValue("id"), seenAt(r))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "conversation not found")
		return
	} else if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) done(w http.ResponseWriter, r *http.Request) {
	err := s.Store.MarkDone(r.Context(), r.PathValue("id"), seenAt(r))
	switch {
	case errors.Is(err, store.ErrStale):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, 404, "conversation not found")
	case err != nil:
		writeErr(w, 500, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeErr(w, 400, "text required")
		return
	}
	conv, err := s.Store.GetConversation(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "conversation not found")
		return
	} else if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	snd, ok := s.Connectors[conv.Source].(connector.Sender)
	if !ok || !s.canSend(conv.Source) {
		writeErr(w, http.StatusNotImplemented, connector.ErrSendUnsupported.Error())
		return
	}

	// Idempotency: the same key always yields the first request's outcome,
	// so a client retrying after a network blip never double-sends.
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key != "" {
		key = conv.ID + "|" + key
		prev, err := s.Store.BeginOp(r.Context(), key)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if prev != nil {
			w.Header().Set("Idempotent-Replayed", "true")
			switch {
			case prev.Status == 0:
				writeErr(w, http.StatusConflict, "同一条消息正在发送中")
			case prev.Status == http.StatusNoContent:
				w.WriteHeader(http.StatusNoContent)
			default:
				writeErr(w, prev.Status, prev.Body)
			}
			return
		}
	}
	// Don't let a client disconnect abort a send that may already be in flight.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	status, msg := http.StatusNoContent, ""
	if err := snd.Send(ctx, conv.NativeID, body.Text); err != nil {
		status, msg = http.StatusBadGateway, err.Error()
		if errors.Is(err, connector.ErrSendUnsupported) {
			status = http.StatusNotImplemented
		}
	}
	if key != "" {
		if err := s.Store.FinishOp(ctx, key, status, msg); err != nil {
			slog.Warn("idempotency receipt", "err", err)
		}
	}
	if status != http.StatusNoContent {
		writeErr(w, status, msg)
		return
	}
	// The sent message shows up on the connector's next sync.
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hits, err := s.Store.Search(r.Context(), q.Get("q"), atoi(q.Get("limit")))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, hits)
}

func (s *Server) whoami(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"user": UserFrom(r.Context())})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	sub := &subscriber{ch: make(chan event, 32), gone: make(chan struct{})}
	s.mu.Lock()
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, sub)
		s.mu.Unlock()
	}()

	write := func(ev event) {
		b, _ := json.Marshal(ev)
		_, _ = w.Write([]byte("id: " + strconv.FormatInt(ev.Seq, 10) + "\nevent: changed\ndata: " + string(b) + "\n\n"))
		fl.Flush()
	}
	_, _ = w.Write([]byte("retry: 3000\n\n"))
	cur, err := s.Store.CommittedSeq(r.Context())
	if err != nil {
		return
	}
	// Resume: EventSource re-sends the last id it saw. If anything changed
	// meanwhile, tell the client right away so it refetches.
	last := r.Header.Get("Last-Event-ID")
	if last == "" {
		last = r.URL.Query().Get("since")
	}
	if n, err := strconv.ParseInt(last, 10, 64); err == nil && n != cur {
		write(event{Seq: cur, Source: ""})
	} else {
		_, _ = w.Write([]byte("id: " + strconv.FormatInt(cur, 10) + "\n: ok\n\n"))
		fl.Flush()
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.gone:
			return
		case ev := <-sub.ch:
			write(ev)
		case <-ping.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			fl.Flush()
		}
	}
}

// spa serves static files and falls back to index.html for client routes.
func spa(root fs.FS) http.Handler {
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(root, p); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "前端未构建：cd web && pnpm install && pnpm build", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/events" {
			slog.Debug("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
		}
	})
}
