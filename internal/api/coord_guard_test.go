package api

import (
	"math"
	"testing"
)

// NaN and ±Inf parse cleanly with strconv and then poison everything
// downstream: `?lat=NaN` produced `distanceKm: NaN` on every located unit,
// json.Marshal refused to encode it, and writeJSON had already sent a 200 — so
// the caller got a success code and an empty body.
func TestCoordinateParsingRejectsNonFiniteValues(t *testing.T) {
	for _, in := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "infinity"} {
		if got := parseCoordPtr(in, 90); got != nil {
			t.Errorf("parseCoordPtr(%q) = %v, want nil", in, *got)
		}
		if got := parseFloatPtr(in); got != nil {
			t.Errorf("parseFloatPtr(%q) = %v, want nil", in, *got)
		}
	}
}

// A longitude of 4000 is not a location. Letting it through means ranking real
// hospitals by their distance from nowhere.
func TestCoordinateParsingRejectsOffPlanetValues(t *testing.T) {
	cases := []struct {
		in    string
		limit float64
	}{
		{"91", 90}, {"-90.5", 90}, {"181", 180}, {"-4000", 180},
	}
	for _, c := range cases {
		if got := parseCoordPtr(c.in, c.limit); got != nil {
			t.Errorf("parseCoordPtr(%q, %v) = %v, want nil", c.in, c.limit, *got)
		}
	}
}

func TestCoordinateParsingKeepsRealPlaces(t *testing.T) {
	for _, c := range []struct {
		in    string
		limit float64
		want  float64
	}{
		{"37.9838", 90, 37.9838}, // Athens
		{"23.7275", 180, 23.7275},
		{"-90", 90, -90}, // the poles are on the planet
		{"180", 180, 180},
		{"0", 90, 0},
	} {
		got := parseCoordPtr(c.in, c.limit)
		if got == nil || math.Abs(*got-c.want) > 1e-9 {
			t.Errorf("parseCoordPtr(%q, %v) = %v, want %v", c.in, c.limit, got, c.want)
		}
	}
}
