package aggregator

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// 2026-06-26 is a Friday → time.Weekday() == 5, matching the getslotsinit `day` field.

func TestGetDoctorSlots_PayloadsAndFiltering(t *testing.T) {
	var initPayload ministry.SlotsInitPayload
	// GetDoctorSlots fans the per-group getactualslots calls out over goroutines,
	// so the mock records them under a lock; without it the appends race and the
	// length assertion below fails intermittently.
	var mu sync.Mutex
	var actualPayloads []ministry.GetActualSlotsPayload

	mock := &MockMinistryClient{
		GetSlotsInitFunc: func(_ context.Context, p ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
			initPayload = p
			return []ministry.SlotGroup{
				{Day: 5, GroupID: 1, GroupColor: "disabled"}, // skipped
				{Day: 5, GroupID: 3, GroupColor: "warning"},  // fetched
				{Day: 5, GroupID: 4, GroupColor: "success"},  // fetched
				{Day: 2, GroupID: 4, GroupColor: "success"},  // other weekday → skipped
			}, nil
		},
		GetActualSlotsFunc: func(_ context.Context, p ministry.GetActualSlotsPayload) ([]ministry.ActualSlot, error) {
			mu.Lock()
			actualPayloads = append(actualPayloads, p)
			mu.Unlock()
			return []ministry.ActualSlot{{RVTime: "14:00", RVDate: "2026-06-26T13:00:00.000+0200", DocName: "ΓΙΑΤΡΟΣ", City: "ΘΕΣΣΑΛΟΝΙΚΗ", RVTName: "ΕΟΠΥΥ"}}, nil
		},
	}

	agg := New(mock)
	pref := 19
	slots, err := agg.GetDoctorSlots(context.Background(), "06026800323", 19, &pref, 3, "2026-06-26")
	if err != nil {
		t.Fatalf("GetDoctorSlots: %v", err)
	}

	// getslotsinit payload: i_amka set, no hunit, foreas 19.
	if initPayload.IAmka == nil || *initPayload.IAmka != "06026800323" {
		t.Errorf("getslotsinit IAmka = %v, want 06026800323", initPayload.IAmka)
	}
	if initPayload.HUnit != nil {
		t.Errorf("getslotsinit HUnit = %v, want nil", *initPayload.HUnit)
	}
	if initPayload.ForeasID != 19 {
		t.Errorf("getslotsinit ForeasID = %d, want 19", initPayload.ForeasID)
	}

	// Only the two non-disabled groups for the target weekday were fetched.
	if len(actualPayloads) != 2 {
		t.Fatalf("getactualslots called %d times, want 2 (skip disabled + other weekday)", len(actualPayloads))
	}
	for _, p := range actualPayloads {
		if p.IAmka == nil || *p.IAmka != "06026800323" {
			t.Errorf("getactualslots IAmka = %v, want 06026800323", p.IAmka)
		}
		if p.HUnit != nil {
			t.Errorf("getactualslots HUnit = %v, want nil (doctor path)", *p.HUnit)
		}
		if p.Foreas != 19 || p.SpecialityID != 3 || p.PrefectureID != 19 {
			t.Errorf("getactualslots foreas/spec/pref = %d/%d/%d, want 19/3/19", p.Foreas, p.SpecialityID, p.PrefectureID)
		}
		if p.DDate != "2026-06-26" {
			t.Errorf("getactualslots DDate = %q, want 2026-06-26 (date-only)", p.DDate)
		}
	}

	if len(slots) != 2 || slots[0].Time != "14:00" {
		t.Errorf("expected 2 flattened slots starting 14:00, got %+v", slots)
	}
}

// The getactualslots endpoint 500s on wrong key casing — lock the JSON contract.
func TestGetActualSlotsPayload_JSONCasing(t *testing.T) {
	amka := "06026800323"
	b, err := json.Marshal(ministry.GetActualSlotsPayload{
		Day: 5, DDate: "2026-06-26", GroupID: 3, Foreas: 19, SpecialityID: 3, PrefectureID: 19, IAmka: &amka,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"foreas":19`, `"specialityId":3`, `"prefectureId":19`, `"i_amka":"06026800323"`} {
		if !strings.Contains(s, want) {
			t.Errorf("payload JSON missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, `"hunit"`) {
		t.Errorf("doctor payload must omit hunit: %s", s)
	}
}
