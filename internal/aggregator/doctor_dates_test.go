package aggregator

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

func TestDoctorFirstDates(t *testing.T) {
	var calls atomic.Int32
	mock := &MockMinistryClient{
		FirstAvailableSlotFunc: func(ctx context.Context, p ministry.SearchPayload) (string, error) {
			calls.Add(1)
			if p.IAmka == nil {
				t.Errorf("probe without i_amka: %+v", p)
				return "", nil
			}
			if p.ForeasID != 19 || p.SpecialityID != 13 || p.HUnit != nil {
				t.Errorf("wrong payload: %+v", p)
			}
			switch *p.IAmka {
			case "A":
				return "2026-09-28", nil
			case "B":
				return "", errors.New("upstream 500")
			}
			return "", nil // no slots in the window
		},
	}
	a := New(mock)
	base := ministry.SearchPayload{SpecialityID: 13, ForeasID: 19, StartDate: "s", EndDate: "e"}
	docs := []ministry.Doctor{{Amka: "A"}, {Amka: "B"}, {Amka: "C"}, {Amka: ""}}

	got := a.DoctorFirstDates(context.Background(), docs, base)
	if len(got) != 4 {
		t.Fatalf("want 4 results, got %d", len(got))
	}
	if got[0].FirstDate == nil || *got[0].FirstDate != "2026-09-28" || !got[0].ScanOK {
		t.Errorf("A: %+v", got[0])
	}
	if got[1].FirstDate != nil || got[1].ScanOK {
		t.Errorf("B (upstream error) must be unknown, not 'no slots': %+v", got[1])
	}
	if got[2].FirstDate != nil || !got[2].ScanOK {
		t.Errorf("C (no slots) must be scanned with no date: %+v", got[2])
	}
	if got[3].FirstDate != nil || got[3].ScanOK {
		t.Errorf("no amka must not be probed: %+v", got[3])
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("want 3 upstream calls, got %d", n)
	}

	// A second identical request is served from cache, except the failed probe.
	a.DoctorFirstDates(context.Background(), docs, base)
	if n := calls.Load(); n != 4 {
		t.Errorf("want only the failed probe retried (4 calls), got %d", n)
	}
}

// The prefecture filter is part of the question, so it is part of the cache key.
func TestDoctorFirstDatesCacheKeepsPrefectureApart(t *testing.T) {
	var calls atomic.Int32
	mock := &MockMinistryClient{
		FirstAvailableSlotFunc: func(ctx context.Context, p ministry.SearchPayload) (string, error) {
			calls.Add(1)
			if p.PrefectureID == nil {
				return "2026-10-01", nil
			}
			return "2026-10-0" + strconv.Itoa(1+*p.PrefectureID), nil
		},
	}
	a := New(mock)
	one, two := 1, 2
	docs := []ministry.Doctor{{Amka: "A"}}
	want := map[*int]string{nil: "2026-10-01", &one: "2026-10-02", &two: "2026-10-03"}
	for round := 0; round < 2; round++ {
		for pref, date := range want {
			base := ministry.SearchPayload{SpecialityID: 13, ForeasID: 19, PrefectureID: pref, StartDate: "s", EndDate: "e"}
			got := a.DoctorFirstDates(context.Background(), docs, base)
			if got[0].FirstDate == nil || *got[0].FirstDate != date {
				t.Errorf("round %d, pref %v: got %v, want %s", round, pref, got[0].FirstDate, date)
			}
		}
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("want 3 probes (one per scope, then cache), got %d", n)
	}
}
