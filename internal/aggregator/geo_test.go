package aggregator

import (
	"math"
	"testing"
)

func TestDistance(t *testing.T) {
	tests := []struct {
		name                   string
		lat1, lon1, lat2, lon2 float64
		want                   float64
		tolerance              float64
	}{
		{
			name: "Athens to Thessaloniki",
			lat1: 37.9838, lon1: 23.7275, // Athens
			lat2: 40.6401, lon2: 22.9444, // Thessaloniki
			want:      300,
			tolerance: 20,
		},
		{
			name: "identical points",
			lat1: 37.9838, lon1: 23.7275,
			lat2: 37.9838, lon2: 23.7275,
			want:      0,
			tolerance: 0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := distance(tt.lat1, tt.lon1, tt.lat2, tt.lon2)
			if math.Abs(got-tt.want) > tt.tolerance {
				t.Errorf("distance() = %.2f km, want %.2f ± %.2f km", got, tt.want, tt.tolerance)
			}
		})
	}
}

func TestHasCoords(t *testing.T) {
	tests := []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"valid Athens", 37.9838, 23.7275, true},
		{"zero/zero is unknown", 0, 0, false},
		{"NaN lat is unknown", math.NaN(), 23.7275, false},
		{"NaN lon is unknown", 37.9838, math.NaN(), false},
		{"valid negative", -33.8688, 151.2093, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasCoords(tt.lat, tt.lon); got != tt.want {
				t.Errorf("hasCoords(%v, %v) = %v, want %v", tt.lat, tt.lon, got, tt.want)
			}
		})
	}
}
