package aggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// MockMinistryClient implements MinistryClient for testing.
type MockMinistryClient struct {
	SearchHUnitsFunc       func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error)
	FirstAvailableSlotFunc func(ctx context.Context, payload ministry.SearchPayload) (string, error)
	GetSpecialtiesFunc     func(ctx context.Context) ([]ministry.Specialty, error)
	GetSlotsInitFunc       func(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error)
}

func (m *MockMinistryClient) SearchHUnits(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
	if m.SearchHUnitsFunc != nil {
		return m.SearchHUnitsFunc(ctx, payload)
	}
	return nil, nil
}

func (m *MockMinistryClient) FirstAvailableSlot(ctx context.Context, payload ministry.SearchPayload) (string, error) {
	if m.FirstAvailableSlotFunc != nil {
		return m.FirstAvailableSlotFunc(ctx, payload)
	}
	return "", nil
}

func (m *MockMinistryClient) GetSpecialties(ctx context.Context) ([]ministry.Specialty, error) {
	if m.GetSpecialtiesFunc != nil {
		return m.GetSpecialtiesFunc(ctx)
	}
	return nil, nil
}

func (m *MockMinistryClient) GetSlotsInit(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
	if m.GetSlotsInitFunc != nil {
		return m.GetSlotsInitFunc(ctx, payload)
	}
	return nil, nil
}

func TestAggregator_SearchUnified(t *testing.T) {
	mockClient := &MockMinistryClient{
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			h1 := 100
			h2 := 200

			if payload.ForeasID == 1 {
				return []ministry.HUnit{
					{HUnit: &h1, Name: "Hospital 1", ForeasID: 1},
					{HUnit: &h2, Name: "Hospital 2", ForeasID: 1},
				}, nil
			} else if payload.ForeasID == 18 {
				h3 := 300
				return []ministry.HUnit{
					{HUnit: &h3, Name: "PFY 1", ForeasID: 18},
				}, nil
			}
			return nil, errors.New("unknown foreas")
		},
	}

	agg := New(mockClient)
	ctx := context.Background()

	pref := 11
	results, err := agg.SearchUnified(ctx, ministry.SearchPayload{SpecialityID: 6, PrefectureID: &pref})
	if err != nil {
		t.Fatalf("SearchUnified failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results (2 Hospitals + 1 PFY), got %d", len(results))
	}

	// Verify both exist
	foundHospital := false
	foundPFY := false
	for _, r := range results {
		if r.ForeasID == 1 {
			foundHospital = true
		}
		if r.ForeasID == 18 {
			foundPFY = true
		}
	}

	if !foundHospital || !foundPFY {
		t.Errorf("Expected to find both Foreas 1 and 18, got Hospital: %v, PFY: %v", foundHospital, foundPFY)
	}
}

func TestAggregator_FastScanner(t *testing.T) {
	mockClient := &MockMinistryClient{
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			if *payload.HUnit == 100 {
				return "2026-05-07", nil
			}
			return "", nil // No slot for others
		},
	}

	agg := New(mockClient)
	ctx := context.Background()

	h1 := 100
	h2 := 200
	units := []ministry.HUnit{
		{HUnit: &h1, Name: "Hospital 1", ForeasID: 1},
		{HUnit: &h2, Name: "Hospital 2", ForeasID: 1}, // This one will return empty
	}

	results := agg.FastScanner(ctx, units, ministry.SearchPayload{})
	if len(results) != 1 {
		t.Fatalf("Expected 1 result with a valid date, got %d", len(results))
	}

	if results[0].FirstDate != "2026-05-07" {
		t.Errorf("Expected 2026-05-07, got %s", results[0].FirstDate)
	}
}

func TestAggregator_GetSpecialties(t *testing.T) {
	callCount := 0
	mockClient := &MockMinistryClient{
		GetSpecialtiesFunc: func(ctx context.Context) ([]ministry.Specialty, error) {
			callCount++
			return []ministry.Specialty{
				{ID: 10, Name: "Neurologist"},
			}, nil
		},
	}

	agg := New(mockClient)
	ctx := context.Background()

	// First call should hit the client
	specs, _ := agg.GetSpecialties(ctx)
	if len(specs) != 1 || callCount != 1 {
		t.Errorf("Expected 1 spec and 1 client call, got %d and %d", len(specs), callCount)
	}
	if specs[0].ID != 10 {
		t.Errorf("Expected ID 10, got %d", specs[0].ID)
	}

	// Second call should hit the cache
	agg.GetSpecialties(ctx)
	if callCount != 1 {
		t.Errorf("Expected cache hit, but client was called again (count: %d)", callCount)
	}
}
