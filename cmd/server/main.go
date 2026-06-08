package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/api"
	"github.com/angelospk/find_doctors_server/internal/ministry"
	"github.com/angelospk/find_doctors_server/internal/watch"
)

func main() {
	logger := newLogger()
	slog.SetDefault(logger)

	logger.Info("initializing ministry api client")
	client := ministry.NewClient("https://www.finddoctors.gov.gr")
	client.SetMaxConcurrency(envInt("MINISTRY_MAX_CONCURRENCY", 30))

	logger.Info("initializing concurrent aggregator engine")
	agg := aggregator.New(client).WithLogger(logger).WithDoctorClient(client)

	logger.Info("initializing cancellation watchdog")
	watchStore, err := watch.NewStore(envString("WATCH_STATE_PATH", "./watchdog-state.json"))
	if err != nil {
		logger.Error("watch store init failed", "error", err)
		os.Exit(1)
	}
	notifiers := []watch.Notifier{watch.NewWebhookNotifier(&http.Client{Timeout: 10 * time.Second})}
	if tok := os.Getenv("TELEGRAM_BOT_TOKEN"); tok != "" {
		notifiers = append(notifiers, watch.NewTelegramNotifier(tok, &http.Client{Timeout: 10 * time.Second}))
	}
	poller := watch.NewPoller(watchStore, client, notifiers, logger)
	poller.Interval = envDuration("WATCH_POLL_INTERVAL", 5*time.Minute)

	server := api.NewServer(agg).WithLogger(logger).WithWatches(watchStore, client)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.HandleHealthz)
	mux.HandleFunc("GET /livez", server.HandleHealthz)
	mux.HandleFunc("GET /readyz", server.HandleReadyz)
	mux.HandleFunc("GET /healthz/upstream", server.HandleUpstreamHealth)

	mux.HandleFunc("GET /api/search", server.HandleSmartSearch)
	mux.HandleFunc("GET /api/specialties", server.HandleGetSpecialties)
	mux.HandleFunc("GET /api/foreas", server.HandleHealthUnitTypes)
	mux.HandleFunc("GET /api/prefectures", server.HandlePrefectures)
	mux.HandleFunc("GET /api/prefectures/covid", server.HandleCovidPrefectures)
	mux.HandleFunc("GET /api/prefectures/mental-health", server.HandleMentalHealthPrefectures)

	mux.HandleFunc("GET /api/doctors/search", server.HandleDoctorSearch)
	mux.HandleFunc("GET /api/doctors/nearby", server.HandleDoctorNearby)
	mux.HandleFunc("GET /api/family-doctors/search", server.HandleFamilyDoctorSearch)
	mux.HandleFunc("GET /api/covid/search", server.HandleCovidSearch)
	mux.HandleFunc("GET /api/mental-health/search", server.HandleMentalHealthSearch)

	mux.HandleFunc("GET /api/machines/types", server.HandleMachineRvTypes)
	mux.HandleFunc("GET /api/machines/search", server.HandleMachineSearch)

	mux.HandleFunc("GET /api/hospitals/{hunitId}/capacity", server.HandleHospitalCapacity)
	mux.HandleFunc("GET /api/hospitals/{hunitId}/slots", server.HandleGranularSlots)
	mux.HandleFunc("GET /api/hospitals/{hunitId}/doors", server.HandleClinicDoors)

	mux.HandleFunc("GET /api/heatmap", server.HandleNationwideHeatmap)

	mux.HandleFunc("POST /api/watches", server.HandleCreateWatch)
	mux.HandleFunc("GET /api/watches/{id}", server.HandleGetWatch)
	mux.HandleFunc("DELETE /api/watches/{id}", server.HandleDeleteWatch)

	// JSON catch-all so unknown routes get the standard error envelope instead
	// of Go's default plain-text "404 page not found".
	mux.HandleFunc("/", api.JSONNotFoundHandler)

	corsCfg := api.CORSConfig{
		AllowedOrigins: corsOrigins(),
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization", "X-Request-Id"},
		MaxAge:         600,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rl := api.NewRateLimiter(ctx, api.RateLimiterConfig{
		RPS:              envFloat("RATE_LIMIT_RPS", 30),
		Burst:            envInt("RATE_LIMIT_BURST", 60),
		BypassPaths:      []string{"/healthz", "/readyz"},
		TrustProxyHeader: envBool("TRUST_PROXY_HEADER", false),
	})

	handler := api.Chain(mux,
		api.RequestIDMiddleware,
		api.RecoveryMiddleware(logger),
		api.LoggingMiddleware(logger),
		api.CORSMiddleware(corsCfg),
		rl.Middleware(),
		api.TimeoutMiddleware(30*time.Second),
	)

	publicAddr := envString("LISTEN_ADDR", ":8080")
	adminAddr := envString("ADMIN_ADDR", ":9090")

	publicSrv := &http.Server{
		Addr:              publicAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	adminMux := http.NewServeMux()
	adminMux.Handle("GET /metrics", promhttp.Handler())
	adminMux.HandleFunc("GET /healthz", server.HandleHealthz)
	adminSrv := &http.Server{
		Addr:              adminAddr,
		Handler:           adminMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErr := make(chan error, 2)
	go func() {
		logger.Info("public api listening", "addr", publicAddr)
		if err := publicSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()
	go func() {
		logger.Info("admin api listening", "addr", adminAddr)
		if err := adminSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	pollerDone := make(chan struct{})
	go func() {
		logger.Info("watchdog poller started", "interval", poller.Interval)
		poller.Run(ctx)
		close(pollerDone)
	}()

	select {
	case err := <-serverErr:
		logger.Error("server failed", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining for up to 30s")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = adminSrv.Shutdown(shutdownCtx)
		if err := publicSrv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}
		select {
		case <-pollerDone:
		case <-shutdownCtx.Done():
			logger.Warn("watchdog poller did not drain within grace window")
		}
		logger.Info("server stopped cleanly")
	}
}

func newLogger() *slog.Logger {
	env := strings.ToLower(os.Getenv("APP_ENV"))
	var h slog.Handler
	if env == "" || env == "dev" || env == "development" {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	return slog.New(h)
}

func corsOrigins() []string {
	raw := os.Getenv("CORS_ALLOWED_ORIGINS")
	if raw == "" {
		return []string{"*"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envString(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		return strings.EqualFold(v, "true") || v == "1"
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
