package aggregator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// probeLog records which units were asked for their first free date.
type probeLog struct {
	mu   sync.Mutex
	ids  []int
	fail map[int]bool
}

func (p *probeLog) client() *MockMinistryClient {
	return &MockMinistryClient{
		FirstAvailableSlotFunc: func(_ context.Context, pl ministry.SearchPayload) (string, error) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.ids = append(p.ids, *pl.HUnit)
			if p.fail[*pl.HUnit] {
				return "", errors.New("upstream timeout")
			}
			return "2026-11-02", nil
		},
	}
}

func (p *probeLog) probed() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := append([]int(nil), p.ids...)
	sort.Ints(out)
	p.ids = nil
	return out
}

func unit(id int, lat, lon float64) ministry.HUnit {
	h := id
	return ministry.HUnit{HUnit: &h, Name: "U", Latitude: lat, Longitude: lon, ForeasID: 1}
}

var athens = [2]float64{37.98, 23.72}

func near(lat, lon float64) SmartSearchOptions {
	return SmartSearchOptions{Lat: &lat, Lon: &lon}
}

// Step 2: the radius is applied BEFORE the date probes. A 10 km search in Athens used to
// probe every unit in Attica; the result was the same, the Ministry load was not.
func TestRadiusIsAppliedBeforeProbing(t *testing.T) {
	log := &probeLog{}
	agg := New(log.client())
	units := []ministry.HUnit{
		unit(1, 37.98, 23.72), // 0 km
		unit(2, 37.97, 23.73), // ~1 km
		unit(3, 38.24, 21.73), // Patras, ~170 km
		unit(4, 0, 0),         // unknown location: kept, as before
	}
	opts := near(athens[0], athens[1])
	opts.MaxDistance = 50
	res := agg.SmartSearch(context.Background(), units, ministry.SearchPayload{SpecialityID: 9}, opts)
	if got := log.probed(); !equalInts(got, []int{1, 2, 4}) {
		t.Fatalf("probed %v, want [1 2 4] (Patras is outside the radius)", got)
	}
	if len(res) != 3 {
		t.Fatalf("results = %d, want 3", len(res))
	}
}

// Step 3: a unit's first date is cached like a doctor's: a correction («την άλλη
// εβδομάδα» keeps the same window only if unchanged), a ladder step or a second user
// does not probe the same units again. A failed probe is not cached.
func TestUnitFirstDatesAreCached(t *testing.T) {
	log := &probeLog{fail: map[int]bool{2: true}}
	agg := New(log.client())
	units := []ministry.HUnit{unit(1, 37.98, 23.72), unit(2, 37.97, 23.73)}
	pl := ministry.SearchPayload{SpecialityID: 9, StartDate: "2026-10-14T00:00:00.000Z", EndDate: "2027-04-14T00:00:00.000Z"}

	agg.SmartSearch(context.Background(), units, pl, SmartSearchOptions{})
	if got := log.probed(); !equalInts(got, []int{1, 2}) {
		t.Fatalf("first search probed %v", got)
	}
	res := agg.SmartSearch(context.Background(), units, pl, SmartSearchOptions{})
	if got := log.probed(); !equalInts(got, []int{2}) {
		t.Fatalf("second search probed %v, want only the failed unit [2]", got)
	}
	for _, r := range res {
		if *r.HUnit.HUnit == 1 && (r.FirstDate == nil || !r.ScanOK) {
			t.Fatalf("cached unit lost its date: %+v", r)
		}
	}

	// Another window is another question.
	pl.EndDate = "2026-10-21T00:00:00.000Z"
	agg.SmartSearch(context.Background(), units, pl, SmartSearchOptions{})
	if got := log.probed(); !equalInts(got, []int{1, 2}) {
		t.Fatalf("new window probed %v, want [1 2]", got)
	}
}

// Step 4: with no radius (nationwide, or the whole prefecture) only the MaxProbes nearest
// units are probed. The 2026-10-13 stall came from one such whole-Attica search.
func TestNoRadiusProbesOnlyTheNearest(t *testing.T) {
	log := &probeLog{}
	agg := New(log.client())
	var units []ministry.HUnit
	for i := 1; i <= 70; i++ { // unit i sits i*0.01° north of Athens: nearer = lower id
		units = append(units, unit(i, athens[0]+float64(i)*0.01, athens[1]))
	}
	// Shuffle the input so "nearest" cannot pass by keeping the input order.
	units[0], units[69] = units[69], units[0]
	units = append(units, unit(999, 0, 0)) // unknown location goes last

	opts := near(athens[0], athens[1])
	opts.MaxProbes = 60
	res, stats := agg.SmartSearchStats(context.Background(), units, ministry.SearchPayload{SpecialityID: 9}, opts)
	got := log.probed()
	want := make([]int, 60)
	for i := range want {
		want[i] = i + 1
	}
	if !equalInts(got, want) {
		t.Fatalf("probed %v, want the 60 nearest (1..60)", got)
	}
	if stats.Candidates != 71 || stats.Probed != 60 {
		t.Fatalf("stats = %+v, want 71 candidates, 60 probed", stats)
	}
	if len(res) != 60 {
		t.Fatalf("results = %d, want 60", len(res))
	}
}

// With no location at all the cap still holds (input order), and 0 means no cap.
func TestProbeCapWithoutOriginAndUnlimited(t *testing.T) {
	log := &probeLog{}
	agg := New(log.client())
	var units []ministry.HUnit
	for i := 1; i <= 10; i++ {
		units = append(units, unit(i, 0, 0))
	}
	_, stats := agg.SmartSearchStats(context.Background(), units, ministry.SearchPayload{}, SmartSearchOptions{MaxProbes: 4})
	if got := log.probed(); !equalInts(got, []int{1, 2, 3, 4}) || stats.Probed != 4 {
		t.Fatalf("probed %v stats %+v, want the first 4", got, stats)
	}
	_, stats = agg.SmartSearchStats(context.Background(), units, ministry.SearchPayload{SpecialityID: 1}, SmartSearchOptions{})
	if stats.Probed != 10 || len(log.probed()) != 10 {
		t.Fatalf("MaxProbes 0 must probe all: %+v", stats)
	}
}

// Step 5: hand-checked coordinates (the Atlas pin fixes) replace a wrong Ministry pin
// before the radius and the distance, so a unit pinned abroad still shows up nearby.
func TestCoordFixesReplaceAWrongPin(t *testing.T) {
	log := &probeLog{}
	agg := New(log.client()).WithCoordFixes(func() map[int]CoordFix {
		return map[int]CoordFix{
			5: {FromLat: 0, FromLon: 0, Lat: 37.975, Lon: 23.735, Approx: false},
			6: {FromLat: 41.0, FromLon: 29.0, Lat: 37.99, Lon: 23.70, Approx: true},
			7: {FromLat: 40.0, FromLon: 22.0, Lat: 37.98, Lon: 23.72}, // Ministry moved the pin since
		}
	})
	units := []ministry.HUnit{
		unit(5, 0, 0),         // no pin → fixed into Athens
		unit(6, 41.0, 29.0),   // pinned in Istanbul → fixed (town centre)
		unit(7, 39.36, 22.94), // pin changed after our check → Ministry wins (Volos)
	}
	opts := near(athens[0], athens[1])
	opts.MaxDistance = 10
	res := agg.SmartSearch(context.Background(), units, ministry.SearchPayload{SpecialityID: 9}, opts)

	byID := map[int]ScannedUnit{}
	for _, r := range res {
		byID[*r.HUnit.HUnit] = r
	}
	if r, ok := byID[5]; !ok || r.Latitude != 37.975 || !r.CoordsFixed || r.ApproxCoords || r.DistanceKm == nil {
		t.Fatalf("unit 5 not fixed: %+v", byID[5])
	}
	if r, ok := byID[6]; !ok || !r.CoordsFixed || !r.ApproxCoords {
		t.Fatalf("unit 6 not fixed as approximate: %+v", byID[6])
	}
	if _, ok := byID[7]; ok {
		t.Fatal("unit 7: a fix for a pin the Ministry has since changed must not apply")
	}
	if got := log.probed(); !equalInts(got, []int{5, 6}) {
		t.Fatalf("probed %v, want [5 6]", got)
	}
	// The caller's slice is not modified.
	if units[0].Latitude != 0 {
		t.Fatal("SmartSearch modified the caller's units")
	}
}

// A town-centre fix is approximate: give it the same slack as doctor-finder does
// (APPROX_SLACK_KM = 10), or the radius drops a unit that may well be inside it.
func TestApproxFixGetsRadiusSlack(t *testing.T) {
	log := &probeLog{}
	agg := New(log.client()).WithCoordFixes(func() map[int]CoordFix {
		return map[int]CoordFix{
			1: {Lat: athens[0] + 0.135, Lon: athens[1], Approx: true},  // ~15 km, town centre
			2: {Lat: athens[0] + 0.135, Lon: athens[1], Approx: false}, // ~15 km, exact
		}
	})
	opts := near(athens[0], athens[1])
	opts.MaxDistance = 10
	agg.SmartSearch(context.Background(), []ministry.HUnit{unit(1, 0, 0), unit(2, 0, 0)}, ministry.SearchPayload{SpecialityID: 9}, opts)
	if got := log.probed(); !equalInts(got, []int{1}) {
		t.Fatalf("probed %v, want [1]: only the approximate pin gets slack", got)
	}
}

// Same slack as doctor-finder: 1.05 × radius + 10 km for a town-centre pin.
func TestApproxSlackMatchesDoctorFinder(t *testing.T) {
	log := &probeLog{}
	agg := New(log.client()).WithCoordFixes(func() map[int]CoordFix {
		return map[int]CoordFix{
			1: {Lat: athens[0] + 0.1814, Lon: athens[1], Approx: true}, // ~20.17 km ≤ 1.05·10+10
			2: {Lat: athens[0] + 0.1934, Lon: athens[1], Approx: true}, // ~21.5 km > 20.5
		}
	})
	opts := near(athens[0], athens[1])
	opts.MaxDistance = 10
	agg.SmartSearch(context.Background(), []ministry.HUnit{unit(1, 0, 0), unit(2, 0, 0)}, ministry.SearchPayload{SpecialityID: 9}, opts)
	if got := log.probed(); !equalInts(got, []int{1}) {
		t.Fatalf("probed %v, want [1]", got)
	}
}

// The cache key is the exact window: two windows on the same days but different times
// are different questions.
func TestUnitDateCacheKeysTheExactWindow(t *testing.T) {
	log := &probeLog{}
	agg := New(log.client())
	units := []ministry.HUnit{unit(1, 37.98, 23.72)}
	pl := ministry.SearchPayload{SpecialityID: 9, StartDate: "2026-10-14T09:00:00.000Z", EndDate: "2027-04-14T00:00:00.000Z"}
	agg.SmartSearch(context.Background(), units, pl, SmartSearchOptions{})
	pl.StartDate = "2026-10-14T15:00:00.000Z"
	agg.SmartSearch(context.Background(), units, pl, SmartSearchOptions{})
	if got := log.probed(); len(got) != 2 {
		t.Fatalf("probed %v, want 2 probes for 2 windows", got)
	}
}

// Probed counts only units that are actually checked (a unit with no id is skipped).
func TestProbedCountsCheckableUnits(t *testing.T) {
	log := &probeLog{}
	agg := New(log.client())
	units := []ministry.HUnit{unit(1, 0, 0), {Name: "no id"}}
	_, stats := agg.SmartSearchStats(context.Background(), units, ministry.SearchPayload{SpecialityID: 3}, SmartSearchOptions{})
	if stats.Probed != 1 || stats.Candidates != 2 {
		t.Fatalf("stats %+v, want 2 candidates, 1 probed", stats)
	}
}

// The fixes come from a private file on disk (never the repo), re-read when it changes.
func TestCoordFixFileReloadsOnChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixes.json")
	load := CoordFixFile(path)
	if got := load(); len(got) != 0 {
		t.Fatalf("missing file must give no fixes, got %v", got)
	}
	write := func(body string, mtime time.Time) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Now().Add(-time.Hour)
	write(`{"fixes":[{"hunit":97,"fromLat":0,"fromLon":0,"lat":39.6,"lon":22.4,"approx":true}]}`, t0)
	got := load()
	if f, ok := got[97]; !ok || f.Lat != 39.6 || !f.Approx {
		t.Fatalf("fix not loaded: %v", got)
	}
	write(`{"fixes":[{"hunit":98,"fromLat":1,"fromLon":2,"lat":38.0,"lon":23.0}]}`, t0.Add(time.Minute))
	got = load()
	if _, ok := got[97]; ok || got[98].Lat != 38.0 {
		t.Fatalf("file change not picked up: %v", got)
	}
	write(`not json`, t0.Add(2*time.Minute))
	if got := load(); got[98].Lat != 38.0 {
		t.Fatalf("a broken file must keep the last good fixes, got %v", got)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
