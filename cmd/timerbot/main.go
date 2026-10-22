package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/vionarion/telegram-timer-bot/internal/bot"
	"github.com/vionarion/telegram-timer-bot/internal/store"
	"github.com/vionarion/telegram-timer-bot/internal/timers"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	httpAddr := env("HTTP_ADDR", ":8080")

	// `timerbot healthcheck` is used by the Docker HEALTHCHECK: the distroless image has no curl.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(httpAddr))
	}

	if err := run(httpAddr); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(httpAddr string) error {
	token := os.Getenv("TELEGRAM_TOKEN")
	if token == "" {
		return errors.New("TELEGRAM_TOKEN is not set")
	}
	loc, err := time.LoadLocation(env("TZ", "Europe/Moscow"))
	if err != nil {
		return fmt.Errorf("TZ: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, env("DB_PATH", "timerbot.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	b, err := bot.New(token, st, loc)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	svc := timers.New(st, b)
	b.SetService(svc)

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Ping(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	srv := &http.Server{Addr: httpAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			stop()
		}
	}()

	svcDone := make(chan error, 1)
	go func() { svcDone <- svc.Run(ctx) }()

	slog.Info("bot started", "http", httpAddr)
	b.Start(ctx) // blocks until SIGTERM

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	return <-svcDone
}

func healthcheck(addr string) int {
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
