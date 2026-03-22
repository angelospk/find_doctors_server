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

	if report.Specialties[0].FillRate != 100.0 {
		t.Errorf("Expected 100.0 fillRate, got %.1f", report.Specialties[0].FillRate)
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
	
	report, _ := agg.HospitalCapacity(context.Background(), 21, 1, nil, specs)
	
	// 2 disabled out of 4 total = 50%
	if report.Specialties[0].FillRate != 50.0 {
		t.Errorf("Expected 50.0 fillRate, got %.1f", report.Specialties[0].FillRate)
	}
}
