package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/api"
	"github.com/angelospk/find_doctors_server/internal/ministry"
)

func main() {
	logger := newLogger()
	slog.SetDefault(logger)

	logger.Info("initializing ministry api client")
	client := ministry.NewClient("https://www.finddoctors.gov.gr")

	logger.Info("initializing concurrent aggregator engine")
	agg := aggregator.New(client).WithLogger(logger).WithDoctorClient(client)

	server := api.NewServer(agg).WithLogger(logger)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.HandleHealthz)
	mux.HandleFunc("GET /readyz", server.HandleReadyz)

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

	corsCfg := api.CORSConfig{
		AllowedOrigins: corsOrigins(),
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization", "X-Request-Id"},
		MaxAge:         600,
	}

	handler := api.Chain(mux,
		api.RequestIDMiddleware,
		api.RecoveryMiddleware(logger),
		api.LoggingMiddleware(logger),
		api.CORSMiddleware(corsCfg),
		api.TimeoutMiddleware(30*time.Second),
	)

	addr := ":8080"
	logger.Info("aggregator backend listening", "addr", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("server failed to start", "error", err)
		os.Exit(1)
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
