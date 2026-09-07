package aggregator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

func firstDateAlways(date string) *MockMinistryClient {
	return &MockMinistryClient{
		FirstAvailableSlotFunc: func(ctx context.Context, payload ministry.SearchPayload) (string, error) {
			return date, nil
		},
	}
}

// The README has documented `distanceKm` on every smart-search result since the
// endpoint shipped. It was never there. A client written against the documented
// shape reads `undefined` and either shows nothing or shows "0 km", and neither
// failure points back at the document that caused it.
func TestScannedUnitCarriesDistanceWhenLocationIsKnown(t *testing.T) {
	near, far := 100, 200
	units := []ministry.HUnit{
		{HUnit: &near, Name: "ΚΟΝΤΑ", Latitude: 37.99, Longitude: 23.73, ForeasID: 1},
		{HUnit: &far, Name: "ΜΑΚΡΙΑ", Latitude: 40.64, Longitude: 22.94, ForeasID: 1}, // Thessaloniki
	}

	lat, lon := 37.9838, 23.7275 // Athens
	out := New(firstDateAlways("2026-09-10")).SmartSearch(
		context.Background(), units, ministry.SearchPayload{},
		SmartSearchOptions{Lat: &lat, Lon: &lon},
	)

	if len(out) != 2 {
		t.Fatalf("want 2 results, got %d", len(out))
	}
	measured := map[string]float64{}
	for _, u := range out {
		if u.DistanceKm == nil {
			t.Fatalf("unit %q has no distanceKm", u.Name)
		}
		measured[u.Name] = *u.DistanceKm
	}
	if measured["ΚΟΝΤΑ"] > 5 {
		t.Errorf("nearby unit measured at %.1f km, want under 5", measured["ΚΟΝΤΑ"])
	}
	if d := measured["ΜΑΚΡΙΑ"]; d < 250 || d > 350 {
		t.Errorf("Thessaloniki measured at %.1f km, want roughly 300", d)
	}
}

// Without an origin there is nothing to measure from, and a zero would read as
// "next door".
func TestScannedUnitOmitsDistanceWithoutALocation(t *testing.T) {
	id := 100
	units := []ministry.HUnit{{HUnit: &id, Name: "ΚΑΠΟΥ", Latitude: 37.99, Longitude: 23.73, ForeasID: 1}}

	out := New(firstDateAlways("2026-09-10")).SmartSearch(
		context.Background(), units, ministry.SearchPayload{}, SmartSearchOptions{},
	)
	if len(out) != 1 {
		t.Fatalf("want 1 result, got %d", len(out))
	}
	if out[0].DistanceKm != nil {
		t.Errorf("distanceKm is %v with no origin given; want nil", *out[0].DistanceKm)
	}

	blob, err := json.Marshal(out[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "distanceKm") {
		t.Errorf("distanceKm serialised anyway: %s", blob)
	}
}

// A unit whose upstream record has no usable coordinates is kept — its location
// is unknown, not distant — but it cannot be given a distance.
func TestScannedUnitOmitsDistanceWhenTheUnitHasNoCoordinates(t *testing.T) {
	id := 100
	units := []ministry.HUnit{{HUnit: &id, Name: "ΑΓΝΩΣΤΟ", Latitude: 0, Longitude: 0, ForeasID: 1}}

	lat, lon := 37.9838, 23.7275
	out := New(firstDateAlways("2026-09-10")).SmartSearch(
		context.Background(), units, ministry.SearchPayload{},
		SmartSearchOptions{Lat: &lat, Lon: &lon},
	)
	if len(out) != 1 {
		t.Fatalf("want 1 result, got %d", len(out))
	}
	if out[0].DistanceKm != nil {
		t.Errorf("a unit at 0,0 was given a distance of %v km", *out[0].DistanceKm)
	}
}
