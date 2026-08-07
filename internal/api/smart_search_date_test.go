package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// stubSlotClient captures the payload passed to FirstAvailableSlot so a test can
// assert the date window (fromDate/toDate) flows into StartDate/EndDate.
// FastScanner calls FirstAvailableSlot from several goroutines at once, so the
// capture is guarded; the unsynchronised version raced under -race.
type stubSlotClient struct {
	mu              sync.Mutex
	lastSlotPayload ministry.SearchPayload
}

func (s *stubSlotClient) lastPayload() ministry.SearchPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSlotPayload
}

func (s *stubSlotClient) SearchHUnits(_ context.Context, p ministry.SearchPayload) ([]ministry.HUnit, error) {
	hunit := 21
	active := 1
	return []ministry.HUnit{{
		HUnitID:  json.RawMessage(`21`),
		HUnit:    &hunit,
		Name:     "ΤΕΣΤ ΜΟΝΑΔΑ",
		ForeasID: p.ForeasID,
		IsActive: &active,
	}}, nil
}
func (s *stubSlotClient) FirstAvailableSlot(_ context.Context, p ministry.SearchPayload) (string, error) {
	s.mu.Lock()
	s.lastSlotPayload = p
	s.mu.Unlock()
	return "2026-07-15", nil
}
func (s *stubSlotClient) GetSpecialties(context.Context) ([]ministry.Specialty, error) {
	return nil, nil
}
func (s *stubSlotClient) GetSlotsInit(context.Context, ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
	return nil, nil
}
func (s *stubSlotClient) GetActualSlots(context.Context, ministry.GetActualSlotsPayload) ([]ministry.ActualSlot, error) {
	return nil, nil
}

func TestHandleSmartSearch_DateWindow(t *testing.T) {
	stub := &stubSlotClient{}
	server := NewServer(aggregator.New(stub))

	req, _ := http.NewRequest("GET", "/api/search?specialtyId=24&fromDate=2026-07-10&toDate=2026-07-20", nil)
	rr := httptest.NewRecorder()
	server.HandleSmartSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if got, want := stub.lastPayload().StartDate, "2026-07-10T00:00:00.000Z"; got != want {
		t.Errorf("StartDate = %q, want %q", got, want)
	}
	// toDate is the exclusive next-day boundary.
	if got, want := stub.lastPayload().EndDate, "2026-07-21T00:00:00.000Z"; got != want {
		t.Errorf("EndDate = %q, want %q", got, want)
	}
}

func TestHandleSmartSearch_NoDateWindowKeepsDefaults(t *testing.T) {
	stub := &stubSlotClient{}
	server := NewServer(aggregator.New(stub))

	req, _ := http.NewRequest("GET", "/api/search?specialtyId=24", nil)
	rr := httptest.NewRecorder()
	server.HandleSmartSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if stub.lastPayload().StartDate == "" || stub.lastPayload().EndDate == "" {
		t.Errorf("expected default StartDate/EndDate, got %q/%q", stub.lastPayload().StartDate, stub.lastPayload().EndDate)
	}
}

func TestHandleSmartSearch_InvalidDate(t *testing.T) {
	stub := &stubSlotClient{}
	server := NewServer(aggregator.New(stub))

	req, _ := http.NewRequest("GET", "/api/search?specialtyId=24&fromDate=notadate", nil)
	rr := httptest.NewRecorder()
	server.HandleSmartSearch(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
