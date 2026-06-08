package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimiter_BlocksAfterBurst(t *testing.T) {
	rl := NewRateLimiter(context.Background(), RateLimiterConfig{RPS: 1, Burst: 2})
	h := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 2 requests within burst should pass, the 3rd should 429.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/search", nil)
		req.RemoteAddr = "1.2.3.4:1111"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		switch i {
		case 0, 1:
			if rr.Code != http.StatusOK {
				t.Errorf("request %d: expected 200, got %d", i, rr.Code)
			}
		case 2:
			if rr.Code != http.StatusTooManyRequests {
				t.Errorf("request %d: expected 429, got %d", i, rr.Code)
			}
		}
	}
}

func TestRateLimiter_DifferentIPsHaveSeparateBuckets(t *testing.T) {
	rl := NewRateLimiter(context.Background(), RateLimiterConfig{RPS: 1, Burst: 1})
	h := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, ip := range []string{"1.1.1.1:1", "2.2.2.2:1"} {
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = ip
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("ip %s should have been allowed once, got %d", ip, rr.Code)
		}
	}
}

func TestRateLimiter_BypassesProbes(t *testing.T) {
	rl := NewRateLimiter(context.Background(), RateLimiterConfig{RPS: 0.001, Burst: 1, BypassPaths: []string{"/healthz", "/readyz"}})
	h := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/healthz", nil)
		req.RemoteAddr = "9.9.9.9:1"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("probe request %d: expected bypass, got %d", i, rr.Code)
		}
	}
}
