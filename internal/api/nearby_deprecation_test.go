package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The upstream geo endpoint has never answered — it rejects every payload with
// a 400 or a 500. Saying so in a header is the difference between "this is
// broken today" and "this has never worked", and only one of those is worth a
// client's retry loop.
func TestDoctorNearbyAnnouncesItselfDeprecated(t *testing.T) {
	server := newServerWithDoctorStub(&stubDoctorClient{})
	rec := httptest.NewRecorder()
	server.HandleDoctorNearby(rec, httptest.NewRequest(
		http.MethodGet, "/api/doctors/nearby?specialtyId=24&lat=37.9&lon=23.7", nil))

	// RFC 9745 wants a structured date. A bare "true" parses as nothing a
	// conforming client can act on.
	if got := rec.Header().Get("Deprecation"); !strings.HasPrefix(got, "@") {
		t.Errorf("Deprecation header is %q, want an @<unix-seconds> date", got)
	}
	if rec.Header().Get("Link") == "" {
		t.Error("no Link header pointing at the endpoint that does work")
	}
}

// A missing parameter still gets its 400, and the deprecation notice still
// travels with it: a client fixing its query should learn both at once.
func TestDoctorNearbyDeprecationSurvivesABadRequest(t *testing.T) {
	server := newServerWithDoctorStub(&stubDoctorClient{})
	rec := httptest.NewRecorder()
	server.HandleDoctorNearby(rec, httptest.NewRequest(
		http.MethodGet, "/api/doctors/nearby?specialtyId=24", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing lat/lon returned %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Deprecation"); !strings.HasPrefix(got, "@") {
		t.Errorf("Deprecation header is %q on the 400 path, want an @<unix-seconds> date", got)
	}
}
