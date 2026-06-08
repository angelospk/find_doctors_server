package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
	"github.com/angelospk/find_doctors_server/internal/watch"
)

type stubSeeder struct {
	date string
	err  error
}

func (s stubSeeder) FirstAvailableSlot(ctx context.Context, _ ministry.SearchPayload) (string, error) {
	return s.date, s.err
}

func newWatchServer(t *testing.T, seeder watch.SlotChecker) *Server {
	t.Helper()
	store, err := watch.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return NewServer(nil).WithWatches(store, seeder)
}

func mux(s *Server) *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("POST /api/watches", s.HandleCreateWatch)
	m.HandleFunc("GET /api/watches/{id}", s.HandleGetWatch)
	m.HandleFunc("DELETE /api/watches/{id}", s.HandleDeleteWatch)
	return m
}

func TestHandleCreateWatch_SeedsAndReturns201(t *testing.T) {
	s := newWatchServer(t, stubSeeder{date: "2026-09-01"})
	body := `{"hunitId":718,"specialtyId":24,"foreasId":1,"prefectureId":5}`
	req := httptest.NewRequest(http.MethodPost, "/api/watches", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux(s).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var got watch.Watch
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" {
		t.Error("expected an id")
	}
	if got.CurrentDate == nil || *got.CurrentDate != "2026-09-01" {
		t.Errorf("expected seeded date, got %v", got.CurrentDate)
	}
	if got.LastNotifiedDate == nil || *got.LastNotifiedDate != "2026-09-01" {
		t.Errorf("expected seeded LastNotifiedDate, got %v", got.LastNotifiedDate)
	}
}

func TestHandleCreateWatch_SeedFailureStillCreates(t *testing.T) {
	s := newWatchServer(t, stubSeeder{err: context.DeadlineExceeded})
	body := `{"hunitId":718,"specialtyId":24,"foreasId":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/watches", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux(s).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 even when seed fails, got %d", rec.Code)
	}
	var got watch.Watch
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.CurrentDate != nil {
		t.Errorf("expected nil CurrentDate after seed failure, got %v", *got.CurrentDate)
	}
}

func TestHandleCreateWatch_MissingSpecialtyIs400(t *testing.T) {
	s := newWatchServer(t, stubSeeder{date: "2026-09-01"})
	req := httptest.NewRequest(http.MethodPost, "/api/watches", strings.NewReader(`{"hunitId":718,"foreasId":1}`))
	rec := httptest.NewRecorder()
	mux(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleCreateWatch_BadWebhookURLIs400(t *testing.T) {
	s := newWatchServer(t, stubSeeder{date: "2026-09-01"})
	body := `{"hunitId":718,"specialtyId":24,"foreasId":1,"webhookUrl":"ftp://nope"}`
	req := httptest.NewRequest(http.MethodPost, "/api/watches", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleGetWatch_RoundTripAnd404(t *testing.T) {
	s := newWatchServer(t, stubSeeder{date: "2026-09-01"})
	created, _ := s.watches.Create(context.Background(), watch.Watch{HUnitID: 1, SpecialtyID: 2})

	req := httptest.NewRequest(http.MethodGet, "/api/watches/"+created.ID, nil)
	rec := httptest.NewRecorder()
	mux(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/watches/unknown", nil)
	rec = httptest.NewRecorder()
	mux(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleDeleteWatch_Returns204(t *testing.T) {
	s := newWatchServer(t, stubSeeder{date: "2026-09-01"})
	created, _ := s.watches.Create(context.Background(), watch.Watch{HUnitID: 1, SpecialtyID: 2})

	req := httptest.NewRequest(http.MethodDelete, "/api/watches/"+created.ID, nil)
	rec := httptest.NewRecorder()
	mux(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}
