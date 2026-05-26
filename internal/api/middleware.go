package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ctxKey is the unexported type used for context keys.
type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
)

// RequestID returns the request id stored in the context, or "" if absent.
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// RequestIDMiddleware honors an inbound X-Request-Id or generates one,
// then stores it on the context and the response header.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fall back to nanosecond timestamp so we never return an all-zero ID.
		ts := time.Now().UnixNano()
		for i := 0; i < 8; i++ {
			b[i] = byte(ts >> (i * 8))
		}
	}
	return hex.EncodeToString(b)
}

// RecoveryMiddleware turns any panic into a 500 JSON error and logs it.
func RecoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						"panic", rec,
						"path", r.URL.Path,
						"method", r.Method,
						"request_id", RequestID(r.Context()),
					)
					writeJSONError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// TimeoutMiddleware enforces a server-side request timeout. The downstream
// handler should respect r.Context() cancellation. On timeout it emits a 504
// using the same JSON error envelope as the rest of the API.
//
// http.TimeoutHandler hard-codes a text/html Content-Type on its 503 body, so
// we wrap it: the inner TimeoutHandler enforces the deadline, and the outer
// handler rewrites the timeout response into a proper JSON 504 envelope.
func TimeoutMiddleware(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		inner := http.TimeoutHandler(next, d, "")
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := &timeoutResponseWriter{ResponseWriter: w}
			inner.ServeHTTP(tw, r)
			if tw.timedOut {
				writeJSONError(w, http.StatusGatewayTimeout, "timeout", "request timed out")
			}
		})
	}
}

// timeoutResponseWriter intercepts the 503 that http.TimeoutHandler emits when a
// request exceeds its deadline, suppressing its body/headers so the wrapper can
// substitute a JSON error envelope. Non-timeout responses pass through untouched.
type timeoutResponseWriter struct {
	http.ResponseWriter
	timedOut    bool
	wroteHeader bool
}

func (w *timeoutResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	// TimeoutHandler writes exactly http.StatusServiceUnavailable on timeout.
	if status == http.StatusServiceUnavailable {
		w.timedOut = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *timeoutResponseWriter) Write(b []byte) (int, error) {
	if w.timedOut {
		// Swallow the default text/html timeout body.
		return len(b), nil
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// CORSConfig drives the CORS middleware.
type CORSConfig struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	MaxAge         int
}

func (c CORSConfig) allowedOrigin(origin string) string {
	if origin == "" {
		return ""
	}
	for _, o := range c.AllowedOrigins {
		if o == "*" || o == origin {
			return o
		}
	}
	return ""
}

// CORSMiddleware adds permissive-but-configurable CORS headers and short-circuits OPTIONS.
func CORSMiddleware(cfg CORSConfig) func(http.Handler) http.Handler {
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			// Always advertise Vary: Origin so caching layers do not serve a
			// response generated for origin A back to origin B.
			w.Header().Set("Vary", "Origin")
			if allow := cfg.allowedOrigin(origin); allow != "" {
				w.Header().Set("Access-Control-Allow-Origin", allow)
				if methods != "" {
					w.Header().Set("Access-Control-Allow-Methods", methods)
				}
				if headers != "" {
					w.Header().Set("Access-Control-Allow-Headers", headers)
				}
				if cfg.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", itoa(cfg.MaxAge))
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func itoa(n int) string {
	// tiny helper to avoid importing strconv just for one place
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// LoggingMiddleware emits one structured log line per request.
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			lw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(lw, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", lw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestID(r.Context()),
			)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Chain composes middleware. The first item in mws is the outermost.
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
