package aggregator

import (
	"context"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

func TestHospitalCapacity_AllDisabled(t *testing.T) {
	hId := 21
	mockClient := &MockMinistryClient{
		GetSlotsInitFunc: func(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
			return []ministry.SlotGroup{
				{GroupColor: "disabled", GroupName: "1"},
				{GroupColor: "disabled", GroupName: "2"},
			}, nil
		},
	}

	agg := New(mockClient)
	specs := []ministry.Specialty{{ID: 6, Name: "Dermatologist"}}

	report, err := agg.HospitalCapacity(context.Background(), hId, 1, nil, specs)
	if err != nil {
		t.Fatalf("HospitalCapacity failed: %v", err)
	}

	if report.HUnitID != hId {
		t.Errorf("Expected HUnitID %d, got %d", hId, report.HUnitID)
	}

	if len(report.Specialties) != 1 {
		t.Fatalf("Expected 1 specialty report, got %d", len(report.Specialties))
	}

	// After #16 fix: every group on the only known day is disabled, which means
	// the doctor isn't on schedule that day — not that the day is fully booked.
	// Old behavior returned 100%; new behavior returns 0 with empty scheduleDays.
	if report.Specialties[0].FillRate != 0.0 {
		t.Errorf("Expected 0.0 fillRate for fully-disabled schedule, got %.1f", report.Specialties[0].FillRate)
	}
	if len(report.Specialties[0].ScheduleDays) != 0 {
		t.Errorf("Expected empty scheduleDays, got %v", report.Specialties[0].ScheduleDays)
	}
}

func TestHospitalCapacity_MixedSlots(t *testing.T) {
	mockClient := &MockMinistryClient{
		GetSlotsInitFunc: func(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
			return []ministry.SlotGroup{
				{GroupColor: "warning", GroupName: "1"},
				{GroupColor: "disabled", GroupName: "2"},
				{GroupColor: "danger", GroupName: "3"},
				{GroupColor: "disabled", GroupName: "4"},
			}, nil
		},
	}

	agg := New(mockClient)
	specs := []ministry.Specialty{{ID: 6, Name: "Dermatologist"}}

	report, err := agg.HospitalCapacity(context.Background(), 21, 1, nil, specs)
	if err != nil {
		t.Fatalf("HospitalCapacity failed: %v", err)
	}

	// All groups land on Day=0; 2 of 4 are disabled → 50% on an active day.
	if report.Specialties[0].FillRate != 50.0 {
		t.Errorf("Expected 50.0 fillRate, got %.1f", report.Specialties[0].FillRate)
	}
}

func TestHospitalCapacity_LimitedSchedule_DoesNotInflateFillRate(t *testing.T) {
	// Regression for #16: the doctor only works Monday and Wednesday.
	// The API returns disabled groups for every other day. Under the old
	// algorithm those days inflated fillRate to ~83%. The new per-day rule
	// should exclude fully-disabled days entirely and report ~33% (1 disabled
	// out of 3 active groups across Mon+Wed), with scheduleDays=[1,3].
	mockClient := &MockMinistryClient{
		GetSlotsInitFunc: func(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
			return []ministry.SlotGroup{
				// Monday: 1 active, 1 disabled (partially booked)
				{Day: 1, GroupColor: "available"},
				{Day: 1, GroupColor: "disabled"},
				// Tuesday: doctor doesn't work
				{Day: 2, GroupColor: "disabled"},
				{Day: 2, GroupColor: "disabled"},
				// Wednesday: 1 active
				{Day: 3, GroupColor: "available"},
				// Thursday: doctor doesn't work
				{Day: 4, GroupColor: "disabled"},
				{Day: 4, GroupColor: "disabled"},
			}, nil
		},
	}

	agg := New(mockClient)
	specs := []ministry.Specialty{{ID: 6, Name: "Cardiology"}}
	report, err := agg.HospitalCapacity(context.Background(), 21, 1, nil, specs)
	if err != nil {
		t.Fatalf("HospitalCapacity failed: %v", err)
	}

	s := report.Specialties[0]
	if s.FillRate < 33.0 || s.FillRate > 34.0 {
		t.Errorf("Expected ~33%% fillRate, got %.2f", s.FillRate)
	}
	if len(s.ScheduleDays) != 2 || s.ScheduleDays[0] != 1 || s.ScheduleDays[1] != 3 {
		t.Errorf("Expected scheduleDays=[1,3], got %v", s.ScheduleDays)
	}
}
