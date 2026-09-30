package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sagehou/MusicForge/internal/forge"
	"github.com/sagehou/MusicForge/web"
)

var version = "development"

func main() {
	configDir := flag.String("config-dir", "/config", "local application data directory")
	health := flag.Bool("healthcheck", false, "check the running server")
	reset := flag.Bool("reset-password-stdin", false, "reset the administrator password from stdin; stop the server first")
	flag.Parse()
	if value := os.Getenv("MUSICFORGE_CONFIG_DIR"); value != "" {
		*configDir = value
	}
	cfg, err := forge.LoadRuntime(*configDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *health {
		_, port, err := net.SplitHostPort(cfg.Listen)
		if err != nil {
			os.Exit(1)
		}
		client := http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
		if err != nil {
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		fmt.Fprintln(os.Stderr, "invalid log level")
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	app, err := forge.New(cfg, logger, version, web.Assets())
	if err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
	defer app.Close()
	if *reset {
		password, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
		if err == nil {
			err = app.ResetPassword(strings.TrimRight(string(password), "\r\n"))
		}
		if err != nil {
			logger.Error("password reset failed", "error", err)
			os.Exit(1)
		}
		logger.Info("administrator password reset; all sessions revoked")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: cfg.Listen, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	app.Start(ctx)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	logger.Info("MusicForge started", "version", version, "listen", cfg.Listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server failed", "error", err)
		stop()
	}
	app.Wait()
}
