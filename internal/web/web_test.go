package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesTheAppAtRoot(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / returned %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type is %q, want text/html", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<html lang=\"el\">") {
		t.Error("the page does not declare itself Greek")
	}
	// The page is useless if it cannot reach the endpoints it is built on.
	for _, path := range []string{"/api/specialties", "/api/prefectures", "/api/search"} {
		if !strings.Contains(body, path) {
			t.Errorf("the page never calls %s", path)
		}
	}
}

// A typo must not silently render the homepage: /api/serch returning the app
// looks like a working request to anything that only checks the status code.
func TestHandlerDoesNotSwallowOtherPaths(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything-else", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /anything-else returned %d, want 404", rec.Code)
	}
}

// The page loads nothing from anywhere but this origin, so it says so — and a
// policy that quietly stops being sent is a policy nobody notices losing.
func TestHandlerSendsATightContentSecurityPolicy(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "connect-src 'self'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q is missing %q", csp, want)
		}
	}
}

// A fixed modtime was the first version of this and it was wrong: it never
// changes, so a rebuilt binary carrying a new page still answered 304 and the
// browser kept showing the old one — which cost an afternoon of "my edit did
// not take". The validator has to follow the content.
func TestHandlerValidatorFollowsTheContent(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag, so a client has nothing to revalidate against")
	}
	if lm := rec.Header().Get("Last-Modified"); lm != "" {
		t.Errorf("Last-Modified is %q; a fixed one outlives the content it describes", lm)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control is %q, want no-cache so the ETag is actually checked", cc)
	}

	// The same content still gets its 304; that is the point of the ETag.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", etag)
	again := httptest.NewRecorder()
	Handler().ServeHTTP(again, req)
	if again.Code != http.StatusNotModified {
		t.Errorf("unchanged content returned %d, want 304", again.Code)
	}
}
