package aggregator

import "testing"

func TestRoundKm(t *testing.T) {
	tests := []struct {
		name string
		km   float64
		want float64
	}{
		{"rounds down", 1.24, 1.2},
		{"half rounds away from zero", 1.25, 1.3},
		{"zero stays zero", 0, 0},
		{"rounds up across integer", 12.98, 13.0},
		{"already one decimal", 3.4, 3.4},
		{"negative half rounds away from zero", -1.25, -1.3},
		{"tiny distance collapses to zero", 0.04, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RoundKm(tt.km); got != tt.want {
				t.Errorf("RoundKm(%v) = %v, want %v", tt.km, got, tt.want)
			}
		})
	}
}
