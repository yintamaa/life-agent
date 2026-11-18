// Package api serves the JSON API, the SSE change feed and the embedded SPA.
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

	mu   sync.Mutex
	subs map[chan string]struct{}
	kick chan string
}

// NewServer creates a server; call Notify on any data/status change.
func NewServer(st *store.Store, conns map[string]connector.Connector, stats map[string]*connector.Status, static fs.FS) *Server {
	s := &Server{Store: st, Connectors: conns, Statuses: stats, Static: static,
		subs: map[chan string]struct{}{}, kick: make(chan string, 64)}
	go s.fanout()
	return s
}

// Notify schedules a "changed" SSE event (coalesced to <= 2/s).
func (s *Server) Notify(source string) {
	select {
	case s.kick <- source:
	default:
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
			s.mu.Lock()
			for src := range pending {
				for ch := range s.subs {
					select {
					case ch <- src:
					default:
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
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusNotFound, "not found") })
	if s.Static != nil {
		mux.Handle("/", spa(s.Static))
	}
	return logRequests(mux)
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
	for i := range rows {
		rows[i].CanSend = s.canSend(rows[i].Source)
	}
	writeJSON(w, rows)
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

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	msgs, err := s.Store.ListMessages(r.Context(), r.PathValue("id"), parseTime(q.Get("before")), atoi(q.Get("limit")))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, msgs)
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.MarkRead(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) done(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.MarkDone(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if !ok {
		writeErr(w, http.StatusNotImplemented, connector.ErrSendUnsupported.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := snd.Send(ctx, conv.NativeID, body.Text); err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, connector.ErrSendUnsupported) {
			code = http.StatusNotImplemented
		}
		writeErr(w, code, err.Error())
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

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := make(chan string, 16)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()
	_, _ = w.Write([]byte(": ok\n\n"))
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case src := <-ch:
			b, _ := json.Marshal(map[string]string{"source": src})
			_, _ = w.Write([]byte("event: changed\ndata: " + string(b) + "\n\n"))
			fl.Flush()
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
