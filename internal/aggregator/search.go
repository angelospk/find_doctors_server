package aggregator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// MinistryClient abstracts the underlying API client for easy mocking.
type MinistryClient interface {
	SearchHUnits(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error)
	FirstAvailableSlot(ctx context.Context, payload ministry.SearchPayload) (string, error)
	GetSpecialties(ctx context.Context) ([]ministry.Specialty, error)
	GetSlotsInit(ctx context.Context, payload ministry.SlotsInitPayload) ([]ministry.SlotGroup, error)
	GetActualSlots(ctx context.Context, payload ministry.GetActualSlotsPayload) ([]ministry.ActualSlot, error)
}

// DoctorClient is the optional doctor-search interface; only the real *ministry.Client
// implements it. Tests can leave it nil to skip doctor-search exercises.
type DoctorClient interface {
	SearchDoctors(ctx context.Context, payload ministry.SearchDoctorsPayload) ([]ministry.Doctor, error)
	SearchDoctorsByLocation(ctx context.Context, payload ministry.SearchDoctorsPayload) ([]ministry.Doctor, error)
	SearchDoctorsFD(ctx context.Context, payload ministry.SearchDoctorsPayload) ([]ministry.Doctor, error)
	SearchHunitsFD(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error)
	GetHealthUnitTypes(ctx context.Context) ([]ministry.HealthUnitType, error)
	GetPrefectures(ctx context.Context) ([]ministry.Prefecture, error)
	GetCovidPrefectures(ctx context.Context) ([]ministry.Prefecture, error)
	GetMentalHealthPrefectures(ctx context.Context) ([]ministry.Prefecture, error)
	GetClinicDoors(ctx context.Context, hunitID, specialtyID int) ([]ministry.ClinicDoor, error)
	GetMachineRvTypes(ctx context.Context) ([]ministry.MachineRvType, error)
	SearchHunitsMachines(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error)
}

// Aggregator coordinates concurrent searches across different entities.
type Aggregator struct {
	client     MinistryClient
	docClient  DoctorClient // optional, may be nil
	logger     *slog.Logger
	specCache  []ministry.Specialty
	specMu     sync.RWMutex
	lastSpecUp time.Time

	// foreasIDsCache + expiry for dynamic foreas discovery (#17).
	foreasMu      sync.RWMutex
	foreasCache   []int
	foreasExpires time.Time

	// Default fallback when dynamic discovery is unavailable.
	defaultForeasIDs []int
}

// New creates a new Aggregator instance using a discard logger by default.
func New(client MinistryClient) *Aggregator {
	return &Aggregator{
		client:           client,
		logger:           slog.New(slog.NewTextHandler(discardWriter{}, nil)),
		defaultForeasIDs: []int{1, 18},
	}
}

// WithLogger attaches a structured logger and returns the aggregator for chaining.
func (a *Aggregator) WithLogger(l *slog.Logger) *Aggregator {
	if l != nil {
		a.logger = l
	}
	return a
}

// WithDoctorClient attaches the extended client used for doctor-search and discovery flows.
func (a *Aggregator) WithDoctorClient(c DoctorClient) *Aggregator {
	a.docClient = c
	return a
}

// DoctorClient returns the extended client, or nil if unset.
func (a *Aggregator) DoctorClient() DoctorClient { return a.docClient }

// Client exposes the underlying ministry client for handlers that need raw access
// (e.g. COVID / Mental Health flag passthrough).
func (a *Aggregator) Client() MinistryClient { return a.client }

// pinger is implemented by clients that can perform a cheap, cache-bypassing
// upstream reachability check (the real *ministry.Client does).
type pinger interface {
	Ping(ctx context.Context) error
}

// Ready performs a live upstream reachability check for readiness probes.
// It bypasses the 24h specialties cache when the client supports Ping, so a
// stale cache cannot keep readiness green during an upstream outage. Clients
// without Ping fall back to GetSpecialties.
func (a *Aggregator) Ready(ctx context.Context) error {
	if p, ok := a.client.(pinger); ok {
		return p.Ping(ctx)
	}
	_, err := a.client.GetSpecialties(ctx)
	return err
}

// SearchUnified runs searches across active foreas types concurrently and merges results.
// Units flagged inactive by the Ministry (IsActive == 0) are dropped so the
// frontend never has to decide whether to show them.
func (a *Aggregator) SearchUnified(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
	foreasIDs := a.activeForeasIDs(ctx, []int{1, 18})
	if len(foreasIDs) == 0 {
		return []ministry.HUnit{}, nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	allUnits := []ministry.HUnit{}
	var errs []error

	for _, fID := range foreasIDs {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			p := payload
			p.ForeasID = id

			units, err := a.client.SearchHUnits(ctx, p)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				a.logger.Warn("foreas search failed", "foreas_id", id, "error", err)
				errs = append(errs, err)
				return
			}
			a.logger.Debug("foreas search result", "foreas_id", id, "count", len(units))
			allUnits = append(allUnits, FilterSearchUnits(units)...)
		}(fID)
	}

	wg.Wait()
	a.logger.Debug("unified search merge", "total", len(allUnits))

	if len(errs) == len(foreasIDs) {
		return nil, errs[0]
	}
	return allUnits, nil
}

// activeForeasIDs returns the foreas list to use for unit-flavored searches.
// If a DoctorClient is wired in, it pulls the live catalog and caches it 24h.
// Falls back to `fallback` on any error so production stays green.
func (a *Aggregator) activeForeasIDs(ctx context.Context, fallback []int) []int {
	if a.docClient == nil {
		return fallback
	}
	a.foreasMu.RLock()
	if time.Now().Before(a.foreasExpires) && len(a.foreasCache) > 0 {
		ids := a.foreasCache
		a.foreasMu.RUnlock()
		return ids
	}
	a.foreasMu.RUnlock()

	types, err := a.docClient.GetHealthUnitTypes(ctx)
	if err != nil || len(types) == 0 {
		a.logger.Warn("health unit types fetch failed, using fallback", "error", err)
		return fallback
	}
	// Only consider unit-typed foreas (1, 18) for /searchhunits. 19 and 20 are
	// for doctor-flavored endpoints — leave them out of the default unit search.
	out := make([]int, 0, len(types))
	for _, t := range types {
		if t.HUnitType == 1 || t.HUnitType == 18 {
			out = append(out, t.HUnitType)
		}
	}
	if len(out) == 0 {
		out = fallback
	}
	a.foreasMu.Lock()
	// Re-check under the write lock: a concurrent caller may have refreshed
	// the cache while we were waiting on upstream. Prefer the existing entry
	// so we don't trample a fresher snapshot.
	if time.Now().Before(a.foreasExpires) && len(a.foreasCache) > 0 {
		cached := a.foreasCache
		a.foreasMu.Unlock()
		return cached
	}
	a.foreasCache = out
	a.foreasExpires = time.Now().Add(24 * time.Hour)
	a.foreasMu.Unlock()
	return out
}

// ScannedUnit represents a health unit with its earliest available appointment date.
// tygo:generate
type ScannedUnit struct {
	ministry.HUnit
	FirstDate *string `json:"firstDate"`
	ScanOK    bool    `json:"scanOk"` // false if the upstream availability check failed
}

// FastScanner concurrently checks the first available slot for a list of units.
func (a *Aggregator) FastScanner(ctx context.Context, units []ministry.HUnit, payload ministry.SearchPayload) []ScannedUnit {
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := []ScannedUnit{}

	sem := make(chan struct{}, 20)

	for _, u := range units {
		if u.HUnit == nil {
			continue
		}

		wg.Add(1)
		go func(unit ministry.HUnit) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			p := payload
			p.HUnit = unit.HUnit
			p.ForeasID = unit.ForeasID

			dateStr, err := a.client.FirstAvailableSlot(ctx, p)
			scanOK := err == nil
			var datePtr *string
			if err == nil && len(dateStr) == 10 {
				datePtr = &dateStr
			}

			a.logger.Debug("first slot probe", "hunit", *unit.HUnit, "name", unit.Name, "date", datePtr)
			mu.Lock()
			results = append(results, ScannedUnit{
				HUnit:     unit,
				FirstDate: datePtr,
				ScanOK:    scanOK,
			})
			mu.Unlock()
		}(u)
	}

	wg.Wait()
	return results
}

// SmartSearchOptions defines search constraints for prioritized results.
type SmartSearchOptions struct {
	Lat         *float64
	Lon         *float64
	MaxDistance float64 // in km
}

// SmartSearch combines availability scanning with geographic filtering and multi-level sorting.
func (a *Aggregator) SmartSearch(ctx context.Context, units []ministry.HUnit, payload ministry.SearchPayload, opts SmartSearchOptions) []ScannedUnit {
	scanned := a.FastScanner(ctx, units, payload)

	var filtered []ScannedUnit
	if opts.MaxDistance > 0 && opts.Lat != nil && opts.Lon != nil {
		for _, u := range scanned {
			d := distance(*opts.Lat, *opts.Lon, u.Latitude, u.Longitude)
			if d <= opts.MaxDistance {
				filtered = append(filtered, u)
			}
		}
	} else {
		filtered = scanned
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].FirstDate == nil && filtered[j].FirstDate == nil {
			return false
		}
		if filtered[i].FirstDate == nil {
			return false
		}
		if filtered[j].FirstDate == nil {
			return true
		}
		if *filtered[i].FirstDate != *filtered[j].FirstDate {
			return *filtered[i].FirstDate < *filtered[j].FirstDate
		}
		if opts.Lat != nil && opts.Lon != nil {
			distI := distance(*opts.Lat, *opts.Lon, filtered[i].Latitude, filtered[i].Longitude)
			distJ := distance(*opts.Lat, *opts.Lon, filtered[j].Latitude, filtered[j].Longitude)
			return distI < distJ
		}
		return false
	})

	return filtered
}

// GranularSlot represents an available appointment time with metadata for the UI.
// tygo:generate
type GranularSlot struct {
	HUnitID   int    `json:"hunitId"`
	Time      string `json:"time"`
	Date      string `json:"date"`
	DayOfWeek int    `json:"dayOfWeek"`
	DocName   string `json:"docName"`
	Address   string `json:"address"`
	City      string `json:"city"`
	GroupID   int    `json:"groupId"`
	Comments  string `json:"comments"`
	RVTName   string `json:"rvtName"`
}

// GranularSlotsOptions controls optional filters for GetGranularSlots.
type GranularSlotsOptions struct {
	CDoorID *int
	// TimeOfDay restricts to one of: "morning" (06-09), "noon" (09-12), "afternoon" (12-15).
	// Empty string means no filter.
	TimeOfDay string
}

// GetGranularSlots fetches and flattens all available appointment times for a unit on a given date.
func (a *Aggregator) GetGranularSlots(ctx context.Context, hunitID, foreasID int, prefID *int, specialtyID int, date string, opts GranularSlotsOptions) ([]GranularSlot, error) {
	parsedDate, err := ParseFlexibleDate(date)
	if err != nil {
		return nil, fmt.Errorf("invalid date format: %w", err)
	}

	startDay := parsedDate.Truncate(24 * time.Hour)
	endDay := startDay.Add(23*time.Hour + 59*time.Minute + 59*time.Second)

	payload := ministry.SlotsInitPayload{
		StartDate:    startDay.Format("2006-01-02T15:04:05.000Z"),
		EndDate:      endDay.Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specialtyID,
		PrefectureID: prefID,
		HUnit:        &hunitID,
		ForeasID:     foreasID,
		CDoorID:      opts.CDoorID,
		IsCovid:      0,
		IsOnlyFd:     0,
		IsMachine:    0,
	}

	groups, err := a.client.GetSlotsInit(ctx, payload)
	if err != nil {
		return nil, err
	}

	type result struct {
		groupID int
		slots   []ministry.ActualSlot
		err     error
	}

	resChan := make(chan result, 12)
	var wg sync.WaitGroup

	for _, g := range groups {
		if g.Day > 0 && g.Day != int(parsedDate.Weekday()) {
			continue
		}
		if g.GroupColor == "" || g.GroupColor == "disabled" {
			continue
		}

		day := g.Day
		wg.Add(1)
		go func(groupID int) {
			defer wg.Done()

			pID := 0
			if prefID != nil {
				pID = *prefID
			}

			p := ministry.GetActualSlotsPayload{
				Day:          day,
				DDate:        startDay.Format("2006-01-02T15:04:05.000Z"),
				GroupID:      groupID,
				HUnit:        &hunitID,
				Foreas:       foreasID,
				SpecialityID: specialtyID,
				PrefectureID: pID,
				IsOnlyFd:     0,
				IsMachine:    0,
				CDoorID:      opts.CDoorID,
			}
			slots, err := a.client.GetActualSlots(ctx, p)
			resChan <- result{groupID: groupID, slots: slots, err: err}
		}(g.GroupID)
	}

	wg.Wait()
	close(resChan)

	var allSlots []GranularSlot
	for r := range resChan {
		if r.err != nil {
			a.logger.Warn("actual slots fetch failed", "group_id", r.groupID, "error", r.err)
			continue
		}

		for _, s := range r.slots {
			gs := GranularSlot{
				HUnitID:   s.HUnitID,
				Time:      s.RVTime,
				Date:      s.RVDate,
				DayOfWeek: int(parsedDate.Weekday()),
				DocName:   s.DocName,
				Address:   s.Address,
				City:      s.City,
				GroupID:   r.groupID,
				Comments:  fmt.Sprintf("%s %s", deref(s.Comments), deref(s.Comments2)),
				RVTName:   s.RVTName,
			}
			if !slotInTimeWindow(gs.Time, opts.TimeOfDay) {
				continue
			}
			allSlots = append(allSlots, gs)
		}
	}

	sort.Slice(allSlots, func(i, j int) bool {
		return allSlots[i].Time < allSlots[j].Time
	})

	return allSlots, nil
}

// GetDoctorSlots fetches available appointment times for a private ΕΟΠΥΥ doctor
// (foreasID 19), keyed by the doctor's ΑΜΚΑ via i_amka (no hunit). Mirrors
// GetGranularSlots but drives the i_amka payloads. Verified live 2026-06-25.
func (a *Aggregator) GetDoctorSlots(ctx context.Context, amka string, foreasID int, prefID *int, specialtyID int, date string) ([]GranularSlot, error) {
	parsedDate, err := ParseFlexibleDate(date)
	if err != nil {
		return nil, fmt.Errorf("invalid date format: %w", err)
	}

	startDay := parsedDate.Truncate(24 * time.Hour)
	endDay := startDay.Add(23*time.Hour + 59*time.Minute + 59*time.Second)

	groups, err := a.client.GetSlotsInit(ctx, ministry.SlotsInitPayload{
		StartDate:    startDay.Format("2006-01-02T15:04:05.000Z"),
		EndDate:      endDay.Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specialtyID,
		PrefectureID: prefID,
		ForeasID:     foreasID,
		IAmka:        &amka,
	})
	if err != nil {
		return nil, err
	}

	pID := 0
	if prefID != nil {
		pID = *prefID
	}

	type result struct {
		groupID int
		slots   []ministry.ActualSlot
		err     error
	}
	resChan := make(chan result, 12)
	var wg sync.WaitGroup

	for _, g := range groups {
		if g.Day > 0 && g.Day != int(parsedDate.Weekday()) {
			continue
		}
		if g.GroupColor == "" || g.GroupColor == "disabled" {
			continue
		}
		wg.Add(1)
		go func(day, groupID int) {
			defer wg.Done()
			// getactualslots wants a date-only ddate + lowercase-d keys (see payload doc).
			slots, err := a.client.GetActualSlots(ctx, ministry.GetActualSlotsPayload{
				Day:          day,
				DDate:        startDay.Format("2006-01-02"),
				GroupID:      groupID,
				Foreas:       foreasID,
				SpecialityID: specialtyID,
				PrefectureID: pID,
				IAmka:        &amka,
			})
			resChan <- result{groupID: groupID, slots: slots, err: err}
		}(g.Day, g.GroupID)
	}

	wg.Wait()
	close(resChan)

	var allSlots []GranularSlot
	for r := range resChan {
		if r.err != nil {
			a.logger.Warn("doctor actual slots fetch failed", "group_id", r.groupID, "amka", amka, "error", r.err)
			continue
		}
		for _, s := range r.slots {
			allSlots = append(allSlots, GranularSlot{
				HUnitID:   s.HUnitID,
				Time:      s.RVTime,
				Date:      s.RVDate,
				DayOfWeek: int(parsedDate.Weekday()),
				DocName:   s.DocName,
				Address:   s.Address,
				City:      s.City,
				GroupID:   r.groupID,
				Comments:  fmt.Sprintf("%s %s", deref(s.Comments), deref(s.Comments2)),
				RVTName:   s.RVTName,
			})
		}
	}

	sort.Slice(allSlots, func(i, j int) bool { return allSlots[i].Time < allSlots[j].Time })
	return allSlots, nil
}

// slotInTimeWindow returns true when t falls in the requested band, or when no
// band is configured. Time strings are H:MM, HH:MM, or HH:MM:SS in 24-hour form.
func slotInTimeWindow(t, band string) bool {
	if band == "" {
		return true
	}
	colon := strings.IndexByte(t, ':')
	if colon <= 0 {
		return false
	}
	h, err := strconv.Atoi(t[:colon])
	if err != nil {
		return false
	}
	switch band {
	case "morning":
		return h >= 6 && h < 9
	case "noon":
		return h >= 9 && h < 12
	case "afternoon":
		return h >= 12 && h < 15
	default:
		// Unknown band → don't filter (caller is responsible for validating).
		return true
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SpecialtyCapacity represents the calculated fill-rate for a single specialty.
// tygo:generate
type SpecialtyCapacity struct {
	ID            int     `json:"specialtyId"`
	Name          string  `json:"name"`
	FillRate      float64 `json:"fillRate"`
	FirstDate     *string `json:"firstDate"`
	ScheduleDays  []int   `json:"scheduleDays"`
	ActiveGroups  int     `json:"activeGroups"`
	DisabledSlots int     `json:"disabledSlots"`
}

// CapacityReport represents the overall capacity for a medical unit.
// tygo:generate
type CapacityReport struct {
	HUnitID     int                 `json:"hunitId"`
	Scanned     int                 `json:"scanned"`
	Specialties []SpecialtyCapacity `json:"specialties"`
}

// HospitalCapacity aggregates capacity across all specialties for a unit.
// The weekly window is computed in Europe/Athens so late-Sunday UTC traffic
// gets the upcoming Monday-Sunday window, not the one that just ended.
func (a *Aggregator) HospitalCapacity(ctx context.Context, hunitID, foreasID int, prefID *int, specialties []ministry.Specialty) (CapacityReport, error) {
	monday, sunday := WeekWindow(time.Now())

	startStr := monday.UTC().Format("2006-01-02T15:04:05.000Z")
	endStr := sunday.UTC().Format("2006-01-02T15:04:05.000Z")

	report := CapacityReport{
		HUnitID:     hunitID,
		Specialties: make([]SpecialtyCapacity, 0),
	}

	type result struct {
		spec ministry.Specialty
		cap  SpecialtyCapacity
		err  error
	}

	resChan := make(chan result, len(specialties))
	sem := make(chan struct{}, 30)

	var wg sync.WaitGroup
	for _, spec := range specialties {
		wg.Add(1)
		go func(s ministry.Specialty) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			payload := ministry.SlotsInitPayload{
				StartDate:    startStr,
				EndDate:      endStr,
				SpecialityID: s.ID,
				PrefectureID: prefID,
				HUnit:        &hunitID,
				ForeasID:     foreasID,
				IsCovid:      0,
				IsOnlyFd:     0,
				IsMachine:    0,
			}

			groups, err := a.client.GetSlotsInit(ctx, payload)
			if err != nil {
				resChan <- result{spec: s, err: err}
				return
			}

			searchPayload := ministry.SearchPayload{
				StartDate:    startStr,
				EndDate:      time.Now().AddDate(0, 6, 0).UTC().Format("2006-01-02T15:04:05.000Z"),
				SpecialityID: s.ID,
				PrefectureID: prefID,
				HUnit:        &hunitID,
				ForeasID:     foreasID,
			}
			firstDate, err := a.client.FirstAvailableSlot(ctx, searchPayload)
			if err != nil {
				a.logger.Warn("first slot probe failed", "hunit", hunitID, "specialty", s.ID, "error", err)
			}
			var datePtr *string
			if len(firstDate) == 10 {
				datePtr = &firstDate
			}

			capRow := SpecialtyCapacity{
				ID:        s.ID,
				Name:      s.Name,
				FirstDate: datePtr,
			}

			if len(groups) == 0 || (len(groups) == 1 && groups[0].ResponseCode == 2) {
				capRow.FillRate = 0.0
				capRow.ScheduleDays = []int{}
				resChan <- result{spec: s, cap: capRow}
				return
			}

			capRow = computeFillRate(capRow, groups)
			resChan <- result{spec: s, cap: capRow}
		}(spec)
	}

	wg.Wait()
	close(resChan)

	for r := range resChan {
		if r.err != nil {
			a.logger.Warn("specialty scan failed", "specialty", r.spec.Name, "id", r.spec.ID, "error", r.err)
			continue
		}
		if r.cap.ID != 0 {
			report.Specialties = append(report.Specialties, r.cap)
		}
	}

	report.Scanned = len(report.Specialties)

	sort.Slice(report.Specialties, func(i, j int) bool {
		return report.Specialties[i].FillRate > report.Specialties[j].FillRate
	})

	return report, nil
}

// computeFillRate implements the #16 fix: per-day grouping with fully-disabled
// days excluded from the denominator, plus an explicit scheduleDays list.
func computeFillRate(row SpecialtyCapacity, groups []ministry.SlotGroup) SpecialtyCapacity {
	type dayStats struct {
		total    int
		disabled int
	}
	perDay := make(map[int]*dayStats)

	for _, g := range groups {
		if g.GroupColor == "" {
			continue
		}
		d, ok := perDay[g.Day]
		if !ok {
			d = &dayStats{}
			perDay[g.Day] = d
		}
		d.total++
		if g.GroupColor == "disabled" {
			d.disabled++
		}
	}

	// Active days are those with at least one non-disabled group.
	activeDays := make([]int, 0, len(perDay))
	activeGroups := 0
	disabledInActive := 0
	for day, s := range perDay {
		if s.total == 0 {
			continue
		}
		if s.disabled == s.total {
			// Day is entirely off — not part of the schedule.
			continue
		}
		activeDays = append(activeDays, day)
		activeGroups += s.total
		disabledInActive += s.disabled
	}
	sort.Ints(activeDays)

	row.ScheduleDays = activeDays
	row.ActiveGroups = activeGroups
	row.DisabledSlots = disabledInActive
	if activeGroups == 0 {
		row.FillRate = 0.0
	} else {
		row.FillRate = (float64(disabledInActive) / float64(activeGroups)) * 100.0
	}
	return row
}

// GetSpecialties returns a cached list of specialties.
func (a *Aggregator) GetSpecialties(ctx context.Context) ([]ministry.Specialty, error) {
	a.specMu.RLock()
	if time.Since(a.lastSpecUp) < 1*time.Hour && len(a.specCache) > 0 {
		defer a.specMu.RUnlock()
		return a.specCache, nil
	}
	a.specMu.RUnlock()

	a.specMu.Lock()
	defer a.specMu.Unlock()

	if time.Since(a.lastSpecUp) < 1*time.Hour && len(a.specCache) > 0 {
		return a.specCache, nil
	}

	specs, err := a.client.GetSpecialties(ctx)
	if err != nil {
		return nil, err
	}

	a.specCache = specs
	a.lastSpecUp = time.Now()
	return specs, nil
}

// discardWriter is a minimal io.Writer that drops everything (used as a slog default sink).
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
