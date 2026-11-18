package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// AuthConfig controls who may reach the hub besides local processes.
//
// Recommended setup (same as OpenClaw): keep `listen` on 127.0.0.1 and put
// `tailscale serve` in front. Requests then arrive from loopback with
// X-Forwarded-For set to the caller's tailnet IP; we resolve that IP with
// `tailscale whois` (never trusting the identity header alone) and allow
// only listed logins.
type AuthConfig struct {
	Tailscale struct {
		Enabled       bool     `yaml:"enabled"`
		CLIPath       string   `yaml:"cli_path"`       // default: tailscale
		AllowedLogins []string `yaml:"allowed_logins"` // e.g. you@github
	} `yaml:"tailscale"`
	// Tokens are bearer tokens for scripts / future remote agents.
	// Sent as "Authorization: Bearer <t>" or "?access_token=<t>" (EventSource).
	Tokens []string `yaml:"tokens"`
}

// Authenticator implements the policy:
//
//	direct loopback, no proxy headers  -> allowed (you, on the hub machine)
//	valid bearer token                  -> allowed
//	proxied (X-Forwarded-For) + tailscale whois login in allowlist -> allowed
//	anything else                       -> 401/403
type Authenticator struct {
	cfg   AuthConfig
	whois func(ctx context.Context, ip string) (whoisResult, error)

	mu    sync.Mutex
	cache map[string]cachedWhois
}

type whoisResult struct {
	Login  string
	Tagged bool
}

type cachedWhois struct {
	res whoisResult
	err error
	exp time.Time
}

type ctxKey struct{}

// UserFrom returns the authenticated identity ("local", "token", or a tailnet login).
func UserFrom(ctx context.Context) string {
	u, _ := ctx.Value(ctxKey{}).(string)
	return u
}

// NewAuthenticator builds the policy from config.
func NewAuthenticator(cfg AuthConfig) *Authenticator {
	if cfg.Tailscale.CLIPath == "" {
		cfg.Tailscale.CLIPath = "tailscale"
	}
	a := &Authenticator{cfg: cfg, cache: map[string]cachedWhois{}}
	a.whois = a.execWhois
	return a
}

func (a *Authenticator) execWhois(ctx context.Context, ip string) (whoisResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, a.cfg.Tailscale.CLIPath, "whois", "--json", ip).Output()
	if err != nil {
		return whoisResult{}, fmt.Errorf("tailscale whois %s: %w", ip, err)
	}
	var v struct {
		Node struct {
			Tags []string `json:"Tags"`
		} `json:"Node"`
		UserProfile struct {
			LoginName string `json:"LoginName"`
		} `json:"UserProfile"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return whoisResult{}, fmt.Errorf("parse whois: %w", err)
	}
	return whoisResult{Login: v.UserProfile.LoginName, Tagged: len(v.Node.Tags) > 0}, nil
}

func (a *Authenticator) lookup(ctx context.Context, ip string) (whoisResult, error) {
	a.mu.Lock()
	c, ok := a.cache[ip]
	a.mu.Unlock()
	if ok && time.Now().Before(c.exp) {
		return c.res, c.err
	}
	res, err := a.whois(ctx, ip)
	a.mu.Lock()
	a.cache[ip] = cachedWhois{res: res, err: err, exp: time.Now().Add(time.Minute)}
	a.mu.Unlock()
	return res, err
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *Authenticator) tokenOK(r *http.Request) bool {
	t := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if t == "" || t == r.Header.Get("Authorization") {
		t = r.URL.Query().Get("access_token")
	}
	if t == "" {
		return false
	}
	for _, want := range a.cfg.Tokens {
		if want != "" && subtle.ConstantTimeCompare([]byte(t), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

// Authorize returns the identity for r, or an HTTP status and reason.
func (a *Authenticator) Authorize(r *http.Request) (user string, status int, reason string) {
	if a.tokenOK(r) {
		return "token", 0, ""
	}
	xff := r.Header.Get("X-Forwarded-For")
	if isLoopback(r.RemoteAddr) && xff == "" && r.Header.Get("Forwarded") == "" {
		return "local", 0, ""
	}
	if xff == "" || !isLoopback(r.RemoteAddr) {
		// Non-loopback peers only get in with a token; we never trust
		// forwarding headers from a peer that isn't our local proxy.
		return "", http.StatusUnauthorized, "需要 token，或通过 tailscale serve 访问"
	}
	if !a.cfg.Tailscale.Enabled {
		return "", http.StatusForbidden, "经代理访问需要开启 auth.tailscale"
	}
	ip := strings.TrimSpace(strings.Split(xff, ",")[0])
	if net.ParseIP(ip) == nil {
		return "", http.StatusForbidden, "无法识别来源地址"
	}
	res, err := a.lookup(r.Context(), ip)
	if err != nil {
		return "", http.StatusForbidden, "无法验证 Tailscale 身份"
	}
	if hdr := r.Header.Get("Tailscale-User-Login"); hdr != "" && !strings.EqualFold(hdr, res.Login) {
		return "", http.StatusForbidden, "Tailscale 身份头与来源不一致"
	}
	for _, l := range a.cfg.Tailscale.AllowedLogins {
		if l != "" && strings.EqualFold(l, res.Login) && !res.Tagged {
			return res.Login, 0, ""
		}
	}
	return "", http.StatusForbidden, "该 Tailscale 账号未被允许：" + res.Login
}

// Middleware enforces Authorize on every request.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, status, reason := a.Authorize(r)
		if status != 0 {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeErr(w, status, reason)
			} else {
				http.Error(w, reason, status)
			}
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}
