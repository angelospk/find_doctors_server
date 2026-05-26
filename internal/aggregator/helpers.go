package aggregator

import (
	"errors"
	"strings"
	"time"
)

// ParseFlexibleDate accepts the date formats observed in callers:
//   - "2006-01-02"
//   - RFC3339 ("2006-01-02T15:04:05Z" and friends)
//   - the Ministry's millisecond-Z variant: "2006-01-02T15:04:05.000Z"
func ParseFlexibleDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty date")
	}
	layouts := []string{
		"2006-01-02",
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unrecognized date format")
}

// athensLoc is loaded once; falls back to a fixed +02:00 offset when the OS
// tzdata is missing (e.g. distroless containers without /usr/share/zoneinfo).
var athensLoc = func() *time.Location {
	if loc, err := time.LoadLocation("Europe/Athens"); err == nil {
		return loc
	}
	return time.FixedZone("EET-fallback", 2*3600)
}()

// AthensLocation returns the timezone used for week boundary calculations.
func AthensLocation() *time.Location { return athensLoc }

// WeekWindow returns the Monday 00:00 → Sunday 23:59:59 window containing
// `now` in Europe/Athens. Used by HospitalCapacity so the report shows the
// expected week even when the server clock is in UTC and it's late Sunday.
func WeekWindow(now time.Time) (mondayStart, sundayEnd time.Time) {
	local := now.In(athensLoc)
	// Go's Weekday: Sunday=0 .. Saturday=6. Convert to Mon=0..Sun=6.
	wd := (int(local.Weekday()) + 6) % 7
	mondayStart = time.Date(local.Year(), local.Month(), local.Day()-wd, 0, 0, 0, 0, athensLoc)
	sundayEnd = mondayStart.Add(7*24*time.Hour - time.Second)
	return mondayStart, sundayEnd
}

// IsPlaceholder reports whether a Ministry response with the given
// `responseCode` is the "no results" sentinel rather than real data.
func IsPlaceholder(responseCode int) bool {
	return responseCode == 2
}

// SanitizeName returns (cleaned, ok) where ok is false if the name is invalid
// after trimming. Names longer than maxNameLen are rejected outright to
// prevent oversized upstream payloads.
const maxNameLen = 100

func SanitizeName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true // empty is allowed (optional field)
	}
	if len(s) > maxNameLen {
		return "", false
	}
	return s, true
}
