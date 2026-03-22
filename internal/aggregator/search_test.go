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
	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// Order is not guaranteed, find the one with the date
	var found *ScannedUnit
	var missing *ScannedUnit
	for i := range results {
		if results[i].FirstDate != nil {
			found = &results[i]
		} else {
			missing = &results[i]
		}
	}

	if found == nil || *found.FirstDate != "2026-05-07" {
		t.Errorf("Expected 2026-05-07, got %v", found)
	}
	if missing == nil {
		t.Error("Expected one result with nil FirstDate")
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

func TestAggregator_SmartSearch(t *testing.T) {
	h1 := 100
	h2 := 200
	h3 := 300

	units := []ministry.HUnit{
		{HUnit: &h1, Name: "Close Soon", Latitude: 37.98, Longitude: 23.72, ForeasID: 1},      // ~0km
		{HUnit: &h2, Name: "Far Soon", Latitude: 38.24, Longitude: 21.73, ForeasID: 1},       // ~170km (Patras)
		{HUnit: &h3, Name: "Close Late", Latitude: 37.97, Longitude: 23.73, ForeasID: 1},      // ~1km
	}

	mockClient := &MockMinistryClient{
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			if *payload.HUnit == h1 {
				return "2024-05-01", nil
			}
			if *payload.HUnit == h2 {
				return "2024-05-01", nil
			}
			if *payload.HUnit == h3 {
				return "2024-05-10", nil
			}
			return "", nil
		},
	}

	agg := New(mockClient)
	ctx := context.Background()

	t.Run("Distance Filtering", func(t *testing.T) {
		lat, lon := 37.98, 23.72
		opts := SmartSearchOptions{
			Lat:         &lat,
			Lon:         &lon,
			MaxDistance: 50, // Only Athens
		}
		results := agg.SmartSearch(ctx, units, ministry.SearchPayload{}, opts)
		if len(results) != 2 {
			t.Errorf("Expected 2 results within 50km, got %d", len(results))
		}
	})

	t.Run("Multi-Level Sorting", func(t *testing.T) {
		lat, lon := 37.98, 23.72
		opts := SmartSearchOptions{
			Lat:         &lat,
			Lon:         &lon,
			MaxDistance: 0, // No limit
		}
		results := agg.SmartSearch(ctx, units, ministry.SearchPayload{}, opts)
		
		if len(results) != 3 {
			t.Fatalf("Expected 3 results, got %d", len(results))
		}

		// Order should be:
		// 1. Close Soon (2024-05-01, dist 0)
		// 2. Far Soon (2024-05-01, dist 170)
		// 3. Close Late (2024-05-10, dist 1)
		if results[0].Name != "Close Soon" {
			t.Errorf("Expected 1st: Close Soon, got %s", results[0].Name)
		}
		if results[1].Name != "Far Soon" {
			t.Errorf("Expected 2nd: Far Soon, got %s", results[1].Name)
		}
		if results[2].Name != "Close Late" {
			t.Errorf("Expected 3rd: Close Late, got %s", results[2].Name)
		}
	})
}

func TestAggregator_HospitalCapacity_Enhanced(t *testing.T) {
	hID := 70600
	mockClient := &MockMinistryClient{
		GetSlotsInitFunc: func(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
			return []ministry.SlotGroup{
				{GroupColor: "disabled"},
				{GroupColor: "available"},
			}, nil
		},
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			return "2024-06-01", nil
		},
	}

	agg := New(mockClient)
	specs := []ministry.Specialty{{ID: 6, Name: "Cardiology"}}

	report, err := agg.HospitalCapacity(context.Background(), hID, 1, nil, specs)
	if err != nil {
		t.Fatalf("HospitalCapacity failed: %v", err)
	}

	if len(report.Specialties) != 1 {
		t.Fatalf("Expected 1 specialty result, got %d", len(report.Specialties))
	}

	s := report.Specialties[0]
	if s.ID != 6 {
		t.Errorf("Expected specialty ID 6, got %d", s.ID)
	}
	if s.FillRate != 50.0 {
		t.Errorf("Expected 50%% fill rate, got %f", s.FillRate)
	}
	if s.FirstDate == nil || *s.FirstDate != "2024-06-01" {
		t.Errorf("Expected date 2024-06-01, got %v", s.FirstDate)
	}
}
