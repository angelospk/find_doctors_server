package aggregator

import (
	"errors"
	"strings"
	"time"

	"github.com/angelospk/find_doctors_server/internal/ministry"
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

// hasUsableHUnitID reports whether a raw hunitId JSON value identifies a real
// upstream record. The Ministry returns placeholders with hunitId of literal
// null, "" or "0" — those entries cannot be followed up via /capacity or
// /slots, so we drop them.
func hasUsableHUnitID(raw []byte) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return false
	}
	// Strip wrapping quotes if it's a JSON string.
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return false
	}
	return true
}

// FilterSearchUnits applies the API-level guarantees that the public endpoints
// promise: inactive units, the placeholder responseCode=2 row, and entries
// with missing/empty/null/"0" hunitId are dropped. Address fields are also
// trimmed of stray whitespace. Used by every handler that surfaces HUnit
// results to clients (#qa-1, #qa-3, #qa-14).
func FilterSearchUnits(units []ministry.HUnit) []ministry.HUnit {
	out := make([]ministry.HUnit, 0, len(units))
	for _, u := range units {
		if u.IsActive != nil && *u.IsActive == 0 {
			continue
		}
		if IsPlaceholder(u.ResponseCode) {
			continue
		}
		if !hasUsableHUnitID(u.HUnitID) {
			continue
		}
		u.Address = strings.TrimSpace(u.Address)
		u.Name = strings.TrimSpace(u.Name)
		u.City = strings.TrimSpace(u.City)
		out = append(out, u)
	}
	return out
}
