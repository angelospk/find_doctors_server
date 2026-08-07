package aggregator

import "testing"

// TestSlotInTimeWindow is a characterization suite: it pins the CURRENT behavior
// of slotInTimeWindow, including the quirks (hour-only parsing, unknown bands
// passing through, malformed input being rejected before the band is looked at).
// Changing any expectation here means the TimeOfDay filter semantics changed.
func TestSlotInTimeWindow(t *testing.T) {
	tests := []struct {
		name string
		time string
		band string
		want bool
	}{
		// No band configured → everything passes, even garbage input.
		{"empty band passes valid time", "10:00", "", true},
		{"empty band passes malformed time", "not-a-time", "", true},
		{"empty band passes empty time", "", "", true},

		// morning = [06:00, 09:00)
		{"morning lower boundary excluded", "05:59", "morning", false},
		{"morning lower boundary included", "06:00", "morning", true},
		{"morning upper boundary included", "08:59", "morning", true},
		{"morning upper boundary excluded", "09:00", "morning", false},

		// noon = [09:00, 12:00)
		{"noon lower boundary excluded", "08:59", "noon", false},
		{"noon lower boundary included", "09:00", "noon", true},
		{"noon upper boundary included", "11:59", "noon", true},
		{"noon upper boundary excluded", "12:00", "noon", false},

		// afternoon = [12:00, 15:00)
		{"afternoon lower boundary excluded", "11:59", "afternoon", false},
		{"afternoon lower boundary included", "12:00", "afternoon", true},
		{"afternoon upper boundary included", "14:59", "afternoon", true},
		{"afternoon upper boundary excluded", "15:00", "afternoon", false},

		// H:MM (single-digit hour) form.
		{"H:MM morning in band", "8:59", "morning", true},
		{"H:MM morning out of band", "9:30", "morning", false},
		{"H:MM noon in band", "9:30", "noon", true},
		{"H:MM afternoon out of band", "6:00", "afternoon", false},

		// HH:MM:SS form — only the hour is parsed, seconds are ignored.
		{"HH:MM:SS morning in band", "08:59:59", "morning", true},
		{"HH:MM:SS noon in band", "09:00:00", "noon", true},
		{"HH:MM:SS afternoon out of band", "15:00:00", "afternoon", false},

		// Minutes never matter: 08:59:59 is morning, 09:00:00 is not.
		{"minutes ignored within hour", "08:00", "morning", true},
		{"minutes ignored across hour", "09:59", "morning", false},

		// Missing colon → rejected.
		{"no colon at all", "0900", "morning", false},
		{"bare hour", "9", "noon", false},
		{"leading colon (colon at index 0)", ":30", "morning", false},
		{"only a colon", ":", "morning", false},

		// Non-numeric hour → rejected.
		{"non-numeric hour", "ab:30", "morning", false},
		{"empty time with band", "", "morning", false},
		{"whitespace-padded hour", " 9:30", "noon", false},

		// Unknown band → pass-through, but only after the time parses.
		{"unknown band with parseable time", "03:00", "evening", true},
		{"unknown band with out-of-any-band time", "23:00", "evening", true},
		{"unknown band still rejects malformed time", "garbage", "evening", false},
		{"unknown band is case sensitive", "08:30", "Morning", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slotInTimeWindow(tt.time, tt.band); got != tt.want {
				t.Errorf("slotInTimeWindow(%q, %q) = %v, want %v", tt.time, tt.band, got, tt.want)
			}
		})
	}
}
