// Command lifeagent runs the hub: all IM connectors, the store and the web UI
// in one process (meant for the always-on Mac).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yintamaa/life-agent/internal/api"
	"github.com/yintamaa/life-agent/internal/config"
	"github.com/yintamaa/life-agent/internal/connector"
	"github.com/yintamaa/life-agent/internal/connector/discord"
	"github.com/yintamaa/life-agent/internal/connector/feishu"
	"github.com/yintamaa/life-agent/internal/connector/mail"
	"github.com/yintamaa/life-agent/internal/connector/qq"
	"github.com/yintamaa/life-agent/internal/connector/wechat"
	"github.com/yintamaa/life-agent/internal/node"
	"github.com/yintamaa/life-agent/internal/push"
	"github.com/yintamaa/life-agent/internal/store"
	"github.com/yintamaa/life-agent/internal/triage"
	"github.com/yintamaa/life-agent/internal/work"
	"github.com/yintamaa/life-agent/web"
)

func main() {
	// `lifeagent hook`: Claude Code PreToolUse hook (approvals). Must never fail open.
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		os.Exit(node.RunHook(os.Stdin, os.Stdout, 0))
	}
	// `lifeagent node -config node.yaml`: standalone node dialing a remote hub.
	if len(os.Args) > 1 && os.Args[1] == "node" {
		fs := flag.NewFlagSet("node", flag.ExitOnError)
		path := fs.String("config", "node.yaml", "path to node config")
		_ = fs.Parse(os.Args[2:])
		if err := runNode(*path); err != nil {
			slog.Error("fatal", "err", err)
			os.Exit(1)
		}
		return
	}
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	debug := flag.Bool("debug", false, "verbose logging")
	flag.Parse()
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	if err := run(*cfgPath); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	tri, err := triage.New(cfg.Triage)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "lifeagent.db"), tri)
	if err != nil {
		return err
	}
	defer st.Close()

	var conns []connector.Connector
	if cfg.WeChat.Enabled {
		conns = append(conns, wechat.New(cfg.WeChat))
	}
	if cfg.Feishu.Enabled {
		conns = append(conns, feishu.New(cfg.Feishu))
	}
	if cfg.QQ.Enabled {
		conns = append(conns, qq.New(cfg.QQ))
	}
	if cfg.Discord.Enabled {
		conns = append(conns, discord.New(cfg.Discord))
	}
	for _, a := range cfg.Mail {
		conns = append(conns, mail.New(a))
	}

	byID := map[string]connector.Connector{}
	stats := map[string]*connector.Status{}
	srv := api.NewServer(st, byID, stats, web.Dist())
	srv.Auth = api.NewAuthenticator(cfg.Auth)
	if host, _, _ := net.SplitHostPort(cfg.Listen); host != "127.0.0.1" && host != "localhost" && host != "::1" && len(cfg.Auth.Tokens) == 0 {
		slog.Warn("listen 不是本机回环地址，且未配置 auth.tokens：非本机请求都会被拒绝。推荐保持 127.0.0.1 + tailscale serve", "listen", cfg.Listen)
	}
	st.OnChange = srv.Notify
	for _, c := range conns {
		byID[c.ID()] = c
		id := c.ID()
		stats[id] = connector.NewStatus(c, func() { srv.Notify(id) })
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Agent orchestration: the hub always has a task manager; remote nodes can
	// attach later. The hub machine itself can also run agents (in-process node).
	srv.Work = work.New(st)
	srv.Work.Presets = cfg.Agents
	if !cfg.Push.Disabled {
		ps, err := push.New(st, cfg.DataDir, cfg.Push.Subject)
		if err != nil {
			return fmt.Errorf("push: %w", err)
		}
		srv.Push = ps
		go ps.Run(ctx)
		srv.Work.OnNotice = func(n work.Notice) { ps.Notify(noticeToPush(n)) }
	}
	if cfg.Node.Enabled {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if cfg.Node.DataDir == "" {
			cfg.Node.DataDir = filepath.Join(cfg.DataDir, "node")
		}
		n, err := node.New(cfg.Node.Config, srv.Work, exe)
		if err != nil {
			return fmt.Errorf("node: %w", err)
		}
		go func() {
			if err := n.Serve(ctx); err != nil {
				slog.Error("node hook server", "err", err)
			}
		}()
		srv.Work.Attach(work.LocalLink{N: n})
	}
	for _, c := range conns {
		go func(c connector.Connector) {
			slog.Info("connector start", "source", c.ID())
			if err := c.Run(ctx, st, stats[c.ID()]); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("connector stopped", "source", c.ID(), "err", err)
				stats[c.ID()].Fail(err)
			}
		}(c)
	}
	// Surface connector errors in the log as they change.
	go logStatusErrors(ctx, stats)

	hs := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sh)
	}()
	slog.Info("listening", "url", "http://"+cfg.Listen, "connectors", len(conns))
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func logStatusErrors(ctx context.Context, stats map[string]*connector.Status) {
	last := map[string]string{}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for id, s := range stats {
				snap := s.Snapshot()
				if snap.Error != last[id] {
					if snap.Error != "" {
						slog.Warn("connector error", "source", id, "state", snap.State, "err", snap.Error)
					} else {
						slog.Info("connector recovered", "source", id)
					}
					last[id] = snap.Error
				}
			}
		}
	}
}

func runNode(path string) error {
	cfg, err := config.LoadNode(path)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	n, err := node.New(cfg.Config, nil, exe)
	if err != nil {
		return err
	}
	cl, err := node.NewClient(cfg.HubURL, cfg.Token, n, cfg.DataDir)
	if err != nil {
		return err
	}
	defer cl.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		if err := n.Serve(ctx); err != nil {
			slog.Error("node hook server", "err", err)
			stop()
		}
	}()
	info := n.Info(ctx)
	slog.Info("node starting", "name", info.Name, "hub", cfg.HubURL, "runtimes", len(info.Runtimes), "workspaces", len(info.Workspaces))
	if err := cl.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// noticeToPush turns a work notice into a device notification. Approvals are
// urgent and carry allow/deny actions; the tag makes a newer notice for the
// same task replace the older one instead of piling up.
func noticeToPush(n work.Notice) push.Notification {
	switch n.Kind {
	case "approval":
		return push.Notification{
			Title: "需要审批 · " + n.Title, Body: n.Body, Kind: n.Kind, Urgent: true,
			Tag: "approval-" + n.ID, URL: "#/todo?t=" + n.TaskID,
			Data:    map[string]string{"approval_id": n.ID, "task_id": n.TaskID},
			Actions: []push.Action{{Action: "allow", Title: "允许"}, {Action: "deny", Title: "拒绝"}},
		}
	case "task_failed":
		return push.Notification{Title: "任务失败 · " + n.Title, Body: n.Body, Kind: n.Kind, Tag: "task-" + n.TaskID,
			URL: "#/todo?t=" + n.TaskID, Data: map[string]string{"task_id": n.TaskID}}
	default:
		return push.Notification{Title: "任务完成 · " + n.Title, Body: n.Body, Kind: n.Kind, Tag: "task-" + n.TaskID,
			URL: "#/todo?t=" + n.TaskID, Data: map[string]string{"task_id": n.TaskID}}
	}
}
