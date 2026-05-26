package aggregator

import (
	"testing"
	"time"
)

func TestParseFlexibleDate(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"date only", "2026-05-26", false},
		{"rfc3339", "2026-05-26T10:30:00Z", false},
		{"ministry ms-z", "2026-05-26T10:30:00.000Z", false},
		{"empty", "", true},
		{"junk", "not a date", true},
		{"trims", "  2026-05-26  ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseFlexibleDate(c.input)
			if (err != nil) != c.wantErr {
				t.Errorf("ParseFlexibleDate(%q): err=%v wantErr=%v", c.input, err, c.wantErr)
			}
		})
	}
}

func TestWeekWindow_HandlesLateSundayUTC(t *testing.T) {
	// UTC Sunday 23:00 = Athens Monday 01:00 — the report should show
	// the *new* week (starting that Monday), not the week that just ended.
	utcSundayLate := time.Date(2026, 5, 24, 23, 0, 0, 0, time.UTC)
	monday, sunday := WeekWindow(utcSundayLate)
	if monday.In(athensLoc).Weekday() != time.Monday {
		t.Errorf("expected Monday start, got %s", monday.In(athensLoc).Weekday())
	}
	if monday.In(athensLoc).Day() != 25 {
		t.Errorf("expected May 25 as start day, got %s", monday.In(athensLoc))
	}
	if sunday.Sub(monday) < 6*24*time.Hour {
		t.Errorf("window too short: %s", sunday.Sub(monday))
	}
}

func TestWeekWindow_MidweekUTCStaysSameWeek(t *testing.T) {
	wednesday := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	monday, _ := WeekWindow(wednesday)
	if monday.In(athensLoc).Day() != 25 {
		t.Errorf("expected Monday May 25 as start, got %s", monday.In(athensLoc))
	}
}

func TestIsPlaceholder(t *testing.T) {
	if !IsPlaceholder(2) {
		t.Error("expected responseCode=2 to be placeholder")
	}
	if IsPlaceholder(0) || IsPlaceholder(1) {
		t.Error("expected non-2 codes to not be placeholder")
	}
}

func TestSanitizeName(t *testing.T) {
	got, ok := SanitizeName("  Παπαδόπουλος  ")
	if !ok || got != "Παπαδόπουλος" {
		t.Errorf("expected trim of greek name, got (%q, %v)", got, ok)
	}
	if _, ok := SanitizeName(""); !ok {
		t.Error("empty should be allowed (optional field)")
	}
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	if _, ok := SanitizeName(string(long)); ok {
		t.Error("expected rejection of overly long name")
	}
}
