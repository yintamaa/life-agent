package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yintamaa/life-agent/internal/store"
	"github.com/yintamaa/life-agent/internal/work"
)

func (s *Server) registerWork(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/nodes", s.nodes)
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		if s.workOK(w) {
			writeJSON(w, s.Work.Agents(r.Context()))
		}
	})
	mux.HandleFunc("GET /api/tasks", s.listTasks)
	mux.HandleFunc("POST /api/tasks", s.createTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.getTask)
	mux.HandleFunc("GET /api/tasks/{id}/events", s.taskEvents)
	mux.HandleFunc("POST /api/tasks/{id}/input", s.taskInput)
	mux.HandleFunc("POST /api/tasks/{id}/cancel", s.taskCancel)
	mux.HandleFunc("POST /api/tasks/{id}/close", s.taskClose)
	mux.HandleFunc("GET /api/approvals", s.listApprovals)
	mux.HandleFunc("POST /api/approvals/{id}", s.decideApproval)
	mux.HandleFunc("GET /api/node", func(w http.ResponseWriter, r *http.Request) {
		if s.workOK(w) {
			s.Work.ServeNode(w, r)
		}
	})
}

func (s *Server) workOK(w http.ResponseWriter) bool {
	if s.Work == nil {
		writeErr(w, http.StatusServiceUnavailable, "未启用 Agent 协同（config.yaml 里配置 node）")
		return false
	}
	return true
}

func notFoundOr500(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "not found")
		return
	}
	writeErr(w, 500, err.Error())
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	out := s.Work.Nodes(r.Context())
	if out == nil {
		out = []work.NodeStatus{}
	}
	writeJSON(w, out)
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	ts, err := s.Store.ListTasks(r.Context(), r.URL.Query().Get("status"), atoi(r.URL.Query().Get("limit")))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, ts)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	var req work.CreateReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	// Idempotency: a retried "交给 Agent" must not start two agents.
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key != "" {
		key = "task|" + key
		prev, err := s.Store.BeginOp(r.Context(), key)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if prev != nil {
			w.Header().Set("Idempotent-Replayed", "true")
			if prev.Status == 0 {
				writeErr(w, http.StatusConflict, "任务正在创建中")
				return
			}
			if prev.Status != http.StatusCreated {
				writeErr(w, prev.Status, prev.Body)
				return
			}
			t, err := s.Store.GetTask(r.Context(), prev.Body)
			if err != nil {
				notFoundOr500(w, err)
				return
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(w, t)
			return
		}
	}
	t, err := s.Work.Create(r.Context(), req)
	status, body := http.StatusCreated, t.ID
	if err != nil {
		status, body = http.StatusBadRequest, err.Error()
		if t.ID != "" { // stored but failed to start; surface the task anyway
			status, body = http.StatusBadGateway, err.Error()
		}
	}
	if key != "" {
		_ = s.Store.FinishOp(r.Context(), key, status, body)
	}
	if err != nil {
		writeErr(w, status, body)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, t)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	t, err := s.Store.GetTask(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	writeJSON(w, t)
}

func (s *Server) taskEvents(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	evs, err := s.Store.TaskEvents(r.Context(), r.PathValue("id"), after, atoi(r.URL.Query().Get("limit")))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, evs)
}

func (s *Server) taskInput(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeErr(w, 400, "text required")
		return
	}
	if err := s.Work.Continue(r.Context(), r.PathValue("id"), body.Text); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFoundOr500(w, err)
			return
		}
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) taskCancel(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	if err := s.Work.Cancel(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) taskClose(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	if err := s.Work.Close(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	q := r.URL.Query()
	as, err := s.Store.ListApprovals(r.Context(), q.Get("task_id"), q.Get("pending") == "1")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, as)
}

// decideApproval: first decision wins across devices. A late one gets 409
// with the approval as actually decided, so every device shows the truth.
func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	if !s.workOK(w) {
		return
	}
	var body struct {
		Allow bool `json:"allow"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	who := UserFrom(r.Context())
	if who == "" {
		who = "local"
	}
	a, err := s.Work.Decide(r.Context(), r.PathValue("id"), body.Allow, who)
	switch {
	case errors.Is(err, store.ErrAlreadyDecided):
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "already", "approval": a})
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, 404, "not found")
	case err != nil:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "approval": a})
	default:
		writeJSON(w, a)
	}
}
