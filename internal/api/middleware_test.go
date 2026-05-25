package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestID_GeneratedAndEchoed(t *testing.T) {
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestID(r.Context()) == "" {
			t.Errorf("expected request id in context")
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/x", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Header().Get("X-Request-Id") == "" {
		t.Errorf("expected generated X-Request-Id header")
	}
}

func TestRequestID_PreservesInbound(t *testing.T) {
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get("X-Request-Id"); got != "abc-123" {
		t.Errorf("expected request id preserved, got %q", got)
	}
}

func TestRecovery_PanicReturnsJSON500(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("nope")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/x", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
	var body errorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("expected JSON error body, got %q", rr.Body.String())
	}
	if body.Error.Code == "" {
		t.Errorf("expected non-empty error code")
	}
}

func TestCORS_PreflightAndOrigin(t *testing.T) {
	cfg := CORSConfig{
		AllowedOrigins: []string{"http://localhost:5173"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Content-Type"},
	}
	h := CORSMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Preflight from allowed origin
	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected 204 preflight, got %d", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("expected allowed origin, got %q", got)
	}

	// Disallowed origin → no ACAO header
	req2 := httptest.NewRequest("GET", "/x", nil)
	req2.Header.Set("Origin", "http://evil.example")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no ACAO header for disallowed origin")
	}
}

func TestHealthz_ReturnsOK(t *testing.T) {
	s := NewServer(nil)
	rr := httptest.NewRecorder()
	s.HandleHealthz(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Errorf("expected status:ok payload, got %q", rr.Body.String())
	}
}
