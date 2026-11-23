package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yintamaa/life-agent/internal/push"
	"github.com/yintamaa/life-agent/internal/store"
)

func (s *Server) registerPush(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/push/vapid", func(w http.ResponseWriter, r *http.Request) {
		if s.Push == nil {
			writeErr(w, http.StatusServiceUnavailable, "推送未启用")
			return
		}
		writeJSON(w, map[string]string{"public_key": s.Push.PublicKey()})
	})
	mux.HandleFunc("POST /api/push/subscribe", s.pushSubscribe)
	mux.HandleFunc("POST /api/push/unsubscribe", s.pushUnsubscribe)
	mux.HandleFunc("POST /api/push/test", func(w http.ResponseWriter, r *http.Request) {
		if s.Push == nil {
			writeErr(w, http.StatusServiceUnavailable, "推送未启用")
			return
		}
		n, err := s.Push.Send(r.Context(), push.Notification{Title: "life-agent", Body: "推送已开启：Agent 需要审批或完成任务时会通知你", Kind: "test", Tag: "test", URL: "#/todo"})
		if err != nil && n == 0 {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]int{"delivered": n})
	})
}

func (s *Server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		writeErr(w, http.StatusServiceUnavailable, "推送未启用")
		return
	}
	// Body is a PushSubscription.toJSON(): {endpoint, keys: {p256dh, auth}}.
	var body struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil ||
		!strings.HasPrefix(body.Endpoint, "https://") || body.Keys.P256dh == "" || body.Keys.Auth == "" {
		writeErr(w, 400, "invalid subscription")
		return
	}
	if err := s.Store.SavePushSub(r.Context(), store.PushSub{Endpoint: body.Endpoint, P256dh: body.Keys.P256dh, Auth: body.Keys.Auth, UserAgent: r.UserAgent()}); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil || body.Endpoint == "" {
		writeErr(w, 400, "endpoint required")
		return
	}
	if err := s.Store.DeletePushSub(r.Context(), body.Endpoint); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
