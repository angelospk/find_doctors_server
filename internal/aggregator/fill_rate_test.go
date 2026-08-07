package aggregator

import (
	"math"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// TestComputeFillRate exercises the pure fill-rate function directly, so the
// "fully-disabled days are excluded from the denominator" rule is pinned down
// without going through HospitalCapacity and its mocked client.
func TestComputeFillRate(t *testing.T) {
	tests := []struct {
		name         string
		groups       []ministry.SlotGroup
		wantFillRate float64
		wantDays     []int
		wantActive   int
		wantDisabled int
	}{
		{
			name: "fully disabled day excluded from denominator",
			groups: []ministry.SlotGroup{
				// Day 1 is entirely off — must not count at all.
				{Day: 1, GroupColor: "disabled"},
				{Day: 1, GroupColor: "disabled"},
				// Day 2 has one of two groups disabled.
				{Day: 2, GroupColor: "disabled"},
				{Day: 2, GroupColor: "green"},
			},
			wantFillRate: 50.0,
			wantDays:     []int{2},
			wantActive:   2,
			wantDisabled: 1,
		},
		{
			name: "mixed enabled and disabled across active days",
			groups: []ministry.SlotGroup{
				{Day: 1, GroupColor: "green"},
				{Day: 1, GroupColor: "disabled"},
				{Day: 1, GroupColor: "disabled"},
				{Day: 3, GroupColor: "green"},
			},
			wantFillRate: 50.0, // 2 disabled out of 4 active groups
			wantDays:     []int{1, 3},
			wantActive:   4,
			wantDisabled: 2,
		},
		{
			name: "groups with empty GroupColor are skipped",
			groups: []ministry.SlotGroup{
				{Day: 1, GroupColor: ""},
				{Day: 1, GroupColor: ""},
				{Day: 1, GroupColor: "disabled"},
				{Day: 1, GroupColor: "green"},
				// Day 5 only has colorless groups — it never becomes a day at all.
				{Day: 5, GroupColor: ""},
			},
			wantFillRate: 50.0,
			wantDays:     []int{1},
			wantActive:   2,
			wantDisabled: 1,
		},
		{
			name: "no active groups yields zero percent without dividing by zero",
			groups: []ministry.SlotGroup{
				{Day: 1, GroupColor: "disabled"},
				{Day: 2, GroupColor: "disabled"},
				{Day: 3, GroupColor: ""},
			},
			wantFillRate: 0.0,
			wantDays:     []int{},
			wantActive:   0,
			wantDisabled: 0,
		},
		{
			name:         "no groups at all yields zero percent",
			groups:       []ministry.SlotGroup{},
			wantFillRate: 0.0,
			wantDays:     []int{},
			wantActive:   0,
			wantDisabled: 0,
		},
		{
			name: "schedule days are sorted and deduped",
			groups: []ministry.SlotGroup{
				{Day: 5, GroupColor: "green"},
				{Day: 1, GroupColor: "green"},
				{Day: 5, GroupColor: "disabled"},
				{Day: 3, GroupColor: "green"},
				{Day: 1, GroupColor: "green"},
				{Day: 3, GroupColor: "green"},
			},
			wantFillRate: 100.0 / 6.0, // 1 disabled out of 6 active groups
			wantDays:     []int{1, 3, 5},
			wantActive:   6,
			wantDisabled: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeFillRate(SpecialtyCapacity{ID: 6, Name: "Dermatologist"}, tt.groups)

			if math.Abs(got.FillRate-tt.wantFillRate) > 1e-9 {
				t.Errorf("FillRate = %v, want %v", got.FillRate, tt.wantFillRate)
			}
			if got.ActiveGroups != tt.wantActive {
				t.Errorf("ActiveGroups = %d, want %d", got.ActiveGroups, tt.wantActive)
			}
			if got.DisabledSlots != tt.wantDisabled {
				t.Errorf("DisabledSlots = %d, want %d", got.DisabledSlots, tt.wantDisabled)
			}
			if len(got.ScheduleDays) != len(tt.wantDays) {
				t.Fatalf("ScheduleDays = %v, want %v", got.ScheduleDays, tt.wantDays)
			}
			for i := range tt.wantDays {
				if got.ScheduleDays[i] != tt.wantDays[i] {
					t.Fatalf("ScheduleDays = %v, want %v", got.ScheduleDays, tt.wantDays)
				}
			}

			// Identity fields must survive untouched.
			if got.ID != 6 || got.Name != "Dermatologist" {
				t.Errorf("row identity mutated: %+v", got)
			}
		})
	}
}
