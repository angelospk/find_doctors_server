package aggregator

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"
)

// CoordFix replaces a wrong Ministry pin. It applies only while the Ministry still
// reports FromLat/FromLon for the unit; once the pin changes, the Ministry wins.
// Approx marks a town-centre replacement rather than the building itself.
type CoordFix struct {
	FromLat, FromLon, Lat, Lon float64
	Approx                     bool
}

// WithCoordFixes sets the source of hand-checked unit coordinates, keyed by hunit id.
func (a *Aggregator) WithCoordFixes(src func() map[int]CoordFix) *Aggregator {
	a.coordFixes = src
	return a
}

type coordFixFile struct {
	Fixes []struct {
		HUnit   int     `json:"hunit"`
		FromLat float64 `json:"fromLat"`
		FromLon float64 `json:"fromLon"`
		Lat     float64 `json:"lat"`
		Lon     float64 `json:"lon"`
		Approx  bool    `json:"approx"`
	} `json:"fixes"`
}

// CoordFixFile returns a source that reads fixes from a JSON file, re-reading it only
// when its mtime or size changes. A missing file gives no fixes; a broken file keeps
// the last good ones and logs one warning per changed mtime. Coordinates are never logged.
func CoordFixFile(path string) func() map[int]CoordFix {
	var (
		mu     sync.Mutex
		fixes  = map[int]CoordFix{}
		mtime  time.Time
		size   int64 = -1
		loaded bool
	)
	return func() map[int]CoordFix {
		mu.Lock()
		defer mu.Unlock()
		st, err := os.Stat(path)
		if err != nil {
			if loaded {
				fixes, loaded, mtime, size = map[int]CoordFix{}, false, time.Time{}, -1
			}
			return fixes
		}
		if loaded && st.ModTime().Equal(mtime) && st.Size() == size {
			return fixes
		}
		mtime, size, loaded = st.ModTime(), st.Size(), true
		data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from the operator's own env
		if err != nil {
			slog.Default().Warn("unit coordinate fixes unreadable", "path", path, "error", err)
			return fixes
		}
		var f coordFixFile
		if err := json.Unmarshal(data, &f); err != nil {
			slog.Default().Warn("unit coordinate fixes invalid, keeping the previous ones", "path", path)
			return fixes
		}
		next := make(map[int]CoordFix, len(f.Fixes))
		for _, x := range f.Fixes {
			next[x.HUnit] = CoordFix{FromLat: x.FromLat, FromLon: x.FromLon, Lat: x.Lat, Lon: x.Lon, Approx: x.Approx}
		}
		fixes = next
		return fixes
	}
}
