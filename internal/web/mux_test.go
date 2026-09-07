package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/api"
	"github.com/angelospk/find_doctors_server/internal/web"
)

// The page and the API share one mux, and the pattern that mounts the page
// decides what happens to every path the API does not claim.
//
// `GET /` is a SUBTREE pattern in Go's mux and beats the catch-all `/` for
// every GET, so mounting the page there turned `/api/serch` from a JSON error
// envelope into a plain-text 404 — a change no test of the web handler on its
// own could see, because the handler behaved correctly in isolation.
func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", web.Handler())
	mux.HandleFunc("/", api.JSONNotFoundHandler)
	return mux
}

func TestRootServesTheApp(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / returned %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type is %q, want text/html", ct)
	}
}

func TestAnUnknownPathStillGetsTheErrorEnvelope(t *testing.T) {
	for _, path := range []string{"/api/serch", "/api/hospitals", "/nope", "/api/"} {
		rec := httptest.NewRecorder()
		newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s returned %d, want 404", path, rec.Code)
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("GET %s did not return JSON (%v): %s", path, err, rec.Body.String())
			continue
		}
		if body.Error.Code == "" {
			t.Errorf("GET %s returned JSON without an error code: %s", path, rec.Body.String())
		}
	}
}
