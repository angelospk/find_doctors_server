package aggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// helper to create an int pointer
func intPtr(v int) *int { return &v }

// helper to create a string pointer
func strPtr(s string) *string { return &s }

func TestNationwideHeatmap_GroupsByPrefecture(t *testing.T) {
	pref1, pref2 := 11, 22
	h1, h2, h3 := 1, 2, 3

	mockClient := &MockMinistryClient{
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			return []ministry.HUnit{
				{HUnit: &h1, Name: "Unit1", Prefecture: &pref1, ForeasID: payload.ForeasID},
				{HUnit: &h2, Name: "Unit2", Prefecture: &pref1, ForeasID: payload.ForeasID},
				{HUnit: &h3, Name: "Unit3", Prefecture: &pref2, ForeasID: payload.ForeasID},
			}, nil
		},
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			return "2026-05-01", nil
		},
	}

	agg := New(mockClient)
	report, err := agg.NationwideHeatmap(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.SpecialtyID != 6 {
		t.Errorf("expected SpecialtyID=6, got %d", report.SpecialtyID)
	}
	if len(report.Prefectures) != 2 {
		t.Fatalf("expected 2 prefecture entries, got %d", len(report.Prefectures))
	}

	counts := make(map[int]int)
	for _, p := range report.Prefectures {
		counts[p.PrefectureID] = p.UnitCount
	}
	// SearchUnified calls SearchHUnits twice (foreasID 1 and 18), so units are doubled
	// but we're just verifying grouping structure - both calls return 3 units each
	// so we'll have 6 total units: 4 in pref1, 2 in pref2
	if counts[pref1] == 0 {
		t.Errorf("expected entries for prefecture %d", pref1)
	}
	if counts[pref2] == 0 {
		t.Errorf("expected entries for prefecture %d", pref2)
	}
	// pref1 should have 2x the count of pref2
	if counts[pref1] != counts[pref2]*2 {
		t.Errorf("expected pref1 count (%d) to be 2x pref2 count (%d)", counts[pref1], counts[pref2])
	}
}

func TestNationwideHeatmap_NilPrefectureUsesZero(t *testing.T) {
	someID := 99
	h1, h2 := 10, 20

	mockClient := &MockMinistryClient{
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			return []ministry.HUnit{
				{HUnit: &h1, Name: "NoPrefs", Prefecture: nil, ForeasID: payload.ForeasID},
				{HUnit: &h2, Name: "WithPref", Prefecture: &someID, ForeasID: payload.ForeasID},
			}, nil
		},
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			return "2026-05-01", nil
		},
	}

	agg := New(mockClient)
	report, err := agg.NationwideHeatmap(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Prefectures) != 2 {
		t.Fatalf("expected 2 prefecture entries, got %d", len(report.Prefectures))
	}

	prefIDs := make(map[int]bool)
	for _, p := range report.Prefectures {
		prefIDs[p.PrefectureID] = true
	}
	if !prefIDs[0] {
		t.Error("expected PrefectureID=0 entry for units with nil Prefecture")
	}
	if !prefIDs[someID] {
		t.Errorf("expected PrefectureID=%d entry", someID)
	}
}

func TestNationwideHeatmap_FillRateCalculation(t *testing.T) {
	pref := 5
	h1, h2, h3, h4 := 1, 2, 3, 4

	callCount := 0
	mockClient := &MockMinistryClient{
		// Return only on first call to avoid duplication skewing the ratio
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			// foreasID 1 returns 4 units, foreasID 18 returns empty
			if payload.ForeasID == 1 {
				return []ministry.HUnit{
					{HUnit: &h1, Prefecture: &pref, ForeasID: 1},
					{HUnit: &h2, Prefecture: &pref, ForeasID: 1},
					{HUnit: &h3, Prefecture: &pref, ForeasID: 1},
					{HUnit: &h4, Prefecture: &pref, ForeasID: 1},
				}, nil
			}
			return []ministry.HUnit{}, nil
		},
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			callCount++
			// Only h1 gets a slot; h2, h3, h4 get empty (nil FirstDate)
			if payload.HUnit != nil && *payload.HUnit == h1 {
				return "2026-05-01", nil
			}
			return "", nil
		},
	}

	agg := New(mockClient)
	report, err := agg.NationwideHeatmap(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Prefectures) != 1 {
		t.Fatalf("expected 1 prefecture, got %d", len(report.Prefectures))
	}

	// 3 out of 4 units have no slot → 75%
	got := report.Prefectures[0].AvgFillRate
	if got != 75.0 {
		t.Errorf("expected AvgFillRate=75.0, got %.1f", got)
	}
}

func TestNationwideHeatmap_FirstDateIsMinimum(t *testing.T) {
	pref := 7
	h1, h2, h3 := 1, 2, 3

	mockClient := &MockMinistryClient{
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			if payload.ForeasID == 1 {
				return []ministry.HUnit{
					{HUnit: &h1, Prefecture: &pref, ForeasID: 1},
					{HUnit: &h2, Prefecture: &pref, ForeasID: 1},
					{HUnit: &h3, Prefecture: &pref, ForeasID: 1},
				}, nil
			}
			return []ministry.HUnit{}, nil
		},
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			if payload.HUnit == nil {
				return "", nil
			}
			switch *payload.HUnit {
			case h1:
				return "2026-07-10", nil
			case h2:
				return "2026-05-01", nil // earliest
			case h3:
				return "", nil // no slot
			}
			return "", nil
		},
	}

	agg := New(mockClient)
	report, err := agg.NationwideHeatmap(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Prefectures) != 1 {
		t.Fatalf("expected 1 prefecture, got %d", len(report.Prefectures))
	}
	p := report.Prefectures[0]
	if p.FirstDate == nil {
		t.Fatal("expected non-nil FirstDate")
	}
	if *p.FirstDate != "2026-05-01" {
		t.Errorf("expected FirstDate=2026-05-01, got %s", *p.FirstDate)
	}
}

func TestNationwideHeatmap_FirstDateNilWhenAllNil(t *testing.T) {
	pref := 3
	h1, h2 := 1, 2

	mockClient := &MockMinistryClient{
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			if payload.ForeasID == 1 {
				return []ministry.HUnit{
					{HUnit: &h1, Prefecture: &pref, ForeasID: 1},
					{HUnit: &h2, Prefecture: &pref, ForeasID: 1},
				}, nil
			}
			return []ministry.HUnit{}, nil
		},
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			return "", nil // no slots for anyone
		},
	}

	agg := New(mockClient)
	report, err := agg.NationwideHeatmap(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Prefectures) != 1 {
		t.Fatalf("expected 1 prefecture, got %d", len(report.Prefectures))
	}
	if report.Prefectures[0].FirstDate != nil {
		t.Errorf("expected nil FirstDate, got %s", *report.Prefectures[0].FirstDate)
	}
}

func TestNationwideHeatmap_SortedByFillRateAsc(t *testing.T) {
	// pref A (id=1): 2 units, 0 nil → 0% fill (most open)
	// pref B (id=2): 2 units, 1 nil → 50% fill
	// pref C (id=3): 2 units, 2 nil → 100% fill (most closed)
	prefA, prefB, prefC := 1, 2, 3
	hA1, hA2 := 11, 12
	hB1, hB2 := 21, 22
	hC1, hC2 := 31, 32

	mockClient := &MockMinistryClient{
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			if payload.ForeasID == 1 {
				return []ministry.HUnit{
					{HUnit: &hA1, Prefecture: &prefA, ForeasID: 1},
					{HUnit: &hA2, Prefecture: &prefA, ForeasID: 1},
					{HUnit: &hB1, Prefecture: &prefB, ForeasID: 1},
					{HUnit: &hB2, Prefecture: &prefB, ForeasID: 1},
					{HUnit: &hC1, Prefecture: &prefC, ForeasID: 1},
					{HUnit: &hC2, Prefecture: &prefC, ForeasID: 1},
				}, nil
			}
			return []ministry.HUnit{}, nil
		},
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			if payload.HUnit == nil {
				return "", nil
			}
			switch *payload.HUnit {
			case hA1, hA2:
				return "2026-05-01", nil // pref A: both have slots
			case hB1:
				return "2026-05-01", nil // pref B: one slot
			case hB2, hC1, hC2:
				return "", nil // pref B: one nil; pref C: both nil
			}
			return "", nil
		},
	}

	agg := New(mockClient)
	report, err := agg.NationwideHeatmap(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Prefectures) != 3 {
		t.Fatalf("expected 3 prefectures, got %d", len(report.Prefectures))
	}

	if report.Prefectures[0].PrefectureID != prefA {
		t.Errorf("expected first prefecture to be prefA (%d, 0%% fill), got prefID=%d", prefA, report.Prefectures[0].PrefectureID)
	}
	if report.Prefectures[1].PrefectureID != prefB {
		t.Errorf("expected second prefecture to be prefB (%d, 50%% fill), got prefID=%d", prefB, report.Prefectures[1].PrefectureID)
	}
	if report.Prefectures[2].PrefectureID != prefC {
		t.Errorf("expected third prefecture to be prefC (%d, 100%% fill), got prefID=%d", prefC, report.Prefectures[2].PrefectureID)
	}
}

func TestNationwideHeatmap_SearchError(t *testing.T) {
	mockClient := &MockMinistryClient{
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			return nil, errors.New("upstream down")
		},
	}

	agg := New(mockClient)
	_, err := agg.NationwideHeatmap(context.Background(), 6)
	if err == nil {
		t.Error("expected error when all searches fail, got nil")
	}
}

func TestNationwideHeatmap_EmptyResults(t *testing.T) {
	mockClient := &MockMinistryClient{
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			return []ministry.HUnit{}, nil
		},
	}

	agg := New(mockClient)
	report, err := agg.NationwideHeatmap(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Prefectures == nil {
		t.Error("expected non-nil Prefectures slice, got nil")
	}
	if len(report.Prefectures) != 0 {
		t.Errorf("expected 0 prefectures, got %d", len(report.Prefectures))
	}
}

func TestNationwideHeatmap_ScanErrorExcludedFromDenominator(t *testing.T) {
	// 3 units in the same prefecture:
	//   h1 → scan succeeds, has slot       → available
	//   h2 → scan succeeds, no slot        → full (nil FirstDate, ScanOK=true)
	//   h3 → scan errors                   → must NOT count in denominator
	// Expected AvgFillRate = (1 full / 2 successful scans) * 100 = 50.0
	// Without fix it would be (2 nil / 3 total) * 100 = 66.7
	pref := 5
	h1, h2, h3 := 1, 2, 3

	mockClient := &MockMinistryClient{
		SearchHUnitsFunc: func(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
			if payload.ForeasID == 1 {
				return []ministry.HUnit{
					{HUnit: &h1, Prefecture: &pref, ForeasID: 1},
					{HUnit: &h2, Prefecture: &pref, ForeasID: 1},
					{HUnit: &h3, Prefecture: &pref, ForeasID: 1},
				}, nil
			}
			return []ministry.HUnit{}, nil
		},
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			if payload.HUnit == nil {
				return "", nil
			}
			switch *payload.HUnit {
			case h1:
				return "2026-05-01", nil // slot available
			case h2:
				return "", nil // scan OK, no slot
			case h3:
				return "", errors.New("upstream timeout") // scan failed
			}
			return "", nil
		},
	}

	agg := New(mockClient)
	report, err := agg.NationwideHeatmap(context.Background(), 6)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Prefectures) != 1 {
		t.Fatalf("expected 1 prefecture, got %d", len(report.Prefectures))
	}

	got := report.Prefectures[0].AvgFillRate
	if got != 50.0 {
		t.Errorf("expected AvgFillRate=50.0 (scan error excluded from denominator), got %.1f", got)
	}
	if report.Prefectures[0].UnitCount != 2 {
		t.Errorf("expected UnitCount=2 (only successful scans), got %d", report.Prefectures[0].UnitCount)
	}
}
