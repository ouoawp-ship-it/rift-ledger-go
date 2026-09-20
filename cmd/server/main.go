//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"riftledger/internal/app"
	"riftledger/internal/champion"
	"riftledger/internal/httpapi"
	"riftledger/internal/runtimeconfig"
	"riftledger/internal/sqlite"
	"riftledger/internal/telegram"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func envID(key string) (int64, error) {
	v := os.Getenv(key)
	if v == "" {
		return 0, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil {
		return 0, fmt.Errorf("%s必须为整数", key)
	}
	return n, nil
}
func main() {
	if e := run(); e != nil {
		slog.Error("服务停止", "error", e)
		os.Exit(1)
	}
}
func run() error {
	token := os.Getenv("ADMIN_TOKEN")
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`).MatchString(token) {
		return errors.New("请设置32至128位随机ADMIN_TOKEN；推荐运行scripts/init-env.sh生成，不使用示例密码")
	}
	address := env("LISTEN_ADDR", "127.0.0.1:8080")
	host, _, e := net.SplitHostPort(address)
	if e != nil {
		return e
	}
	ip := net.ParseIP(host)
	if (ip == nil || !ip.IsLoopback()) && os.Getenv("ALLOW_PUBLIC_HTTP") != "true" {
		return errors.New("默认拒绝非回环HTTP监听；容器内部监听需显式ALLOW_PUBLIC_HTTP=true，宿主机仍应仅绑定127.0.0.1")
	}
	dir, e := filepath.Abs(env("DATA_DIR", "./data"))
	if e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	lockFile, e := os.OpenFile(filepath.Join(dir, "server.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lockFile.Close()
	if e = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return errors.New("同一数据目录已有服务进程，拒绝重复启动")
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	syscall.Umask(0077)
	dbPath := filepath.Join(dir, "rift-ledger.db")
	db, e := sqlite.Open(dbPath)
	if e != nil {
		return e
	}
	defer db.Close()
	if e = os.Chmod(dbPath, 0600); e != nil {
		return e
	}
	cfg := app.RuntimeConfig{BotUsername: strings.TrimPrefix(os.Getenv("TG_BOT_USERNAME"), "@"), SupportUsername: strings.TrimPrefix(os.Getenv("TG_SUPPORT_USERNAME"), "@")}
	if cfg.GroupID, e = envID("TG_GROUP_ID"); e != nil {
		return e
	}
	if cfg.TopicID, e = envID("TG_TOPIC_ID"); e != nil {
		return e
	}
	if cfg.NotifyAdminID, e = envID("TG_ADMIN_ID"); e != nil {
		return e
	}
	settings, e := runtimeconfig.Open(filepath.Join(dir, "runtime-settings.json"), runtimeconfig.Config{Token: os.Getenv("TG_BOT_TOKEN"), BotUsername: cfg.BotUsername, GroupID: cfg.GroupID, TopicID: cfg.TopicID, AdminID: cfg.NotifyAdminID, SupportUsername: cfg.SupportUsername, Enabled: os.Getenv("TG_BOT_TOKEN") != ""})
	if e != nil {
		return e
	}
	saved := settings.Current()
	cfg = app.RuntimeConfig{BotUsername: saved.BotUsername, GroupID: saved.GroupID, TopicID: saved.TopicID, NotifyAdminID: saved.AdminID, SupportUsername: saved.SupportUsername}
	botToken := saved.Token
	if !saved.Enabled {
		botToken = ""
		cfg.GroupID = 0
		cfg.NotifyAdminID = 0
	}
	s, e := app.New(db, cfg)
	if e != nil {
		return e
	}
	s.Settings = settings
	s.Champions, e = champion.New(filepath.Join(dir, "champions"))
	if e != nil {
		return e
	}
	if botToken == "" {
		_ = s.BotStatus("未启用Telegram；网页与计算服务可用")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	listener, e := net.Listen("tcp", address)
	if e != nil {
		return e
	}
	server := &http.Server{Handler: httpapi.New(s, token), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	var wg sync.WaitGroup
	if botToken != "" {
		_ = s.BotStatus("正在连接Telegram")
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := telegram.New(botToken).Run(ctx, s); e != nil {
				_ = s.BotStatus("已停止：" + e.Error())
				slog.Error("Telegram接收器停止；网页仍可用", "error", e)
			}
		}()
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	slog.Info("峡谷账房Go服务已启动", "version", app.Version, "listen", address, "sqlite", sqlite.Version(), "telegram_enabled", botToken != "")
	select {
	case <-ctx.Done():
	case e = <-errCh:
		if e != nil && !errors.Is(e, http.ErrServerClosed) {
			slog.Error("HTTP服务异常", "error", e)
		}
		cancel()
	}
	cancel()
	shutdown, done := context.WithTimeout(context.Background(), 20*time.Second)
	defer done()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
	}
	wg.Wait()
	if e != nil && !errors.Is(e, http.ErrServerClosed) {
		return e
	}
	return nil
}
