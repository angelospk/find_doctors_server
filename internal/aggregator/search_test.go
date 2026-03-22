package aggregator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// MockMinistryClient implements MinistryClient for testing.
type MockMinistryClient struct {
	SearchHUnitsFunc       func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error)
	FirstAvailableSlotFunc func(ctx context.Context, payload ministry.SearchPayload) (string, error)
	GetSpecialtiesFunc     func(ctx context.Context) ([]ministry.Specialty, error)
	GetSlotsInitFunc       func(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error)
	GetActualSlotsFunc     func(ctx context.Context, payload ministry.GetActualSlotsPayload) ([]ministry.ActualSlot, error)
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

func (m *MockMinistryClient) GetActualSlots(ctx context.Context, payload ministry.GetActualSlotsPayload) ([]ministry.ActualSlot, error) {
	if m.GetActualSlotsFunc != nil {
		return m.GetActualSlotsFunc(ctx, payload)
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

func TestAggregator_GetGranularSlots(t *testing.T) {
	hUnitID := 21
	foreasID := 1
	specID := 6
	date := "2026-03-23" // Monday

	mockClient := &MockMinistryClient{
		GetSlotsInitFunc: func(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
			return []ministry.SlotGroup{
				{Day: int(time.Monday), GroupID: 1, GroupColor: "disabled", GroupName: "06:00-09:00"},
				{Day: int(time.Monday), GroupID: 2, GroupColor: "available", GroupName: "09:00-12:00"}, // Group 2
				{Day: int(time.Monday), GroupID: 3, GroupColor: "available", GroupName: "12:00-15:00"}, // Group 3
			}, nil
		},
		GetActualSlotsFunc: func(ctx context.Context, payload ministry.GetActualSlotsPayload) ([]ministry.ActualSlot, error) {
			if payload.GroupID == 2 {
				return []ministry.ActualSlot{
					{HUnitID: hUnitID, RVTime: "09:30", RVDate: "2026-05-07T09:30:00Z", DocName: "Dr. Papadopoulos"},
					{HUnitID: hUnitID, RVTime: "10:15", RVDate: "2026-05-07T10:15:00Z", DocName: "Dr. Papadopoulos"},
				}, nil
			}
			if payload.GroupID == 3 {
				return []ministry.ActualSlot{
					{HUnitID: hUnitID, RVTime: "12:00", RVDate: "2026-05-07T12:00:00Z", DocName: "Dr. Ioannou"},
				}, nil
			}
			return nil, nil
		},
	}

	agg := New(mockClient)
	ctx := context.Background()

	slots, err := agg.GetGranularSlots(ctx, hUnitID, foreasID, nil, specID, date)
	if err != nil {
		t.Fatalf("GetGranularSlots failed: %v", err)
	}

	if len(slots) != 3 {
		t.Errorf("Expected 3 slots, got %d", len(slots))
	}

	// Verify order (sorted by time)
	if slots[0].Time != "09:30" || slots[1].Time != "10:15" || slots[2].Time != "12:00" {
		t.Errorf("Slots not sorted correctly: %v", slots)
	}

	// Verify flattening
	if slots[0].DocName != "Dr. Papadopoulos" || slots[2].DocName != "Dr. Ioannou" {
		t.Errorf("Doctor names not preserved correctly")
	}

	// Print results for visual confirmation
	for _, s := range slots {
		t.Logf(" - [%s] %s (Group: %d) (Comments: %s)", s.Time, s.DocName, s.GroupID, s.Comments)
	}
}

func TestAggregator_GetGranularSlots_WithComments(t *testing.T) {
	mockClient := &MockMinistryClient{
		GetSlotsInitFunc: func(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
			return []ministry.SlotGroup{
				{Day: 1, GroupID: 1, GroupName: "08:00-11:00", GroupColor: "green"},
			}, nil
		},
		GetActualSlotsFunc: func(ctx context.Context, payload ministry.GetActualSlotsPayload) ([]ministry.ActualSlot, error) {
			if payload.GroupID == 1 {
				return []ministry.ActualSlot{
					{
						HUnitID: 123,
						RVDate:  "2026-03-23T08:30:00.000+0200",
						RVTime:  "08:30",
						DocName: "Dr. Commentator",
						Address: "123 Comment St",
						City:    "Literal City",
						Comments: func(s string) *string { return &s }("Requires medical record."),
						Comments2: func(s string) *string { return &s }("Please arrive 15m early."),
						RVTName: "Specialized",
					},
				}, nil
			}
			return nil, nil
		},
	}

	agg := New(mockClient)
	ctx := context.Background()

	// Monday, 2026-03-23
	slots, err := agg.GetGranularSlots(ctx, 123, 1, nil, 1, "2026-03-23")
	if err != nil {
		t.Fatalf("Failed to get slots: %v", err)
	}

	if len(slots) != 1 {
		t.Fatalf("Expected 1 slot, got %d", len(slots))
	}

	expectedComment := "Requires medical record. Please arrive 15m early."
	if slots[0].Comments != expectedComment {
		t.Errorf("Expected comments %q, got %q", expectedComment, slots[0].Comments)
	}

	if slots[0].RVTName != "Specialized" {
		t.Errorf("Expected RVTName %q, got %q", "Specialized", slots[0].RVTName)
	}

	t.Logf("Result: %v", slots[0])
}
