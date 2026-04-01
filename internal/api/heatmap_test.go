package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// heatmapMock satisfies aggregator.MinistryClient for heatmap handler tests.
type heatmapMock struct {
	searchFunc func(ctx context.Context, p ministry.SearchPayload) ([]ministry.HUnit, error)
	slotFunc   func(ctx context.Context, p ministry.SearchPayload) (string, error)
}

func (m *heatmapMock) SearchHUnits(ctx context.Context, p ministry.SearchPayload) ([]ministry.HUnit, error) {
	if m.searchFunc != nil {
		return m.searchFunc(ctx, p)
	}
	return []ministry.HUnit{}, nil
}

func (m *heatmapMock) FirstAvailableSlot(ctx context.Context, p ministry.SearchPayload) (string, error) {
	if m.slotFunc != nil {
		return m.slotFunc(ctx, p)
	}
	return "", nil
}

func (m *heatmapMock) GetSpecialties(ctx context.Context) ([]ministry.Specialty, error) {
	return nil, nil
}

func (m *heatmapMock) GetSlotsInit(ctx context.Context, p ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
	return nil, nil
}

func (m *heatmapMock) GetActualSlots(ctx context.Context, p ministry.GetActualSlotsPayload) ([]ministry.ActualSlot, error) {
	return nil, nil
}

func TestHandleNationwideHeatmap_MissingSpecialtyID(t *testing.T) {
	srv := NewServer(aggregator.New(&heatmapMock{}))
	req := httptest.NewRequest(http.MethodGet, "/api/heatmap", nil)
	w := httptest.NewRecorder()

	srv.HandleNationwideHeatmap(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleNationwideHeatmap_InvalidSpecialtyID(t *testing.T) {
	srv := NewServer(aggregator.New(&heatmapMock{}))
	req := httptest.NewRequest(http.MethodGet, "/api/heatmap?specialtyId=abc", nil)
	w := httptest.NewRecorder()

	srv.HandleNationwideHeatmap(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleNationwideHeatmap_Success(t *testing.T) {
	pref := 5
	h1, h2 := 1, 2

	mock := &heatmapMock{
		searchFunc: func(ctx context.Context, p ministry.SearchPayload) ([]ministry.HUnit, error) {
			if p.ForeasID == 1 {
				return []ministry.HUnit{
					{HUnit: &h1, Prefecture: &pref, ForeasID: 1},
					{HUnit: &h2, Prefecture: &pref, ForeasID: 1},
				}, nil
			}
			return []ministry.HUnit{}, nil
		},
		slotFunc: func(ctx context.Context, p ministry.SearchPayload) (string, error) {
			return "2026-05-01", nil
		},
	}

	srv := NewServer(aggregator.New(mock))
	req := httptest.NewRequest(http.MethodGet, "/api/heatmap?specialtyId=6", nil)
	w := httptest.NewRecorder()

	srv.HandleNationwideHeatmap(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type=application/json, got %q", ct)
	}

	var report aggregator.HeatmapReport
	if err := json.NewDecoder(w.Body).Decode(&report); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}

	if report.SpecialtyID != 6 {
		t.Errorf("expected SpecialtyID=6, got %d", report.SpecialtyID)
	}
	if report.Prefectures == nil {
		t.Fatal("expected non-nil Prefectures slice")
	}
	if len(report.Prefectures) == 0 {
		t.Fatal("expected at least one prefecture in response")
	}
	// Both units have slots → AvgFillRate should be 0
	if report.Prefectures[0].AvgFillRate != 0.0 {
		t.Errorf("expected AvgFillRate=0.0, got %.1f", report.Prefectures[0].AvgFillRate)
	}
}
