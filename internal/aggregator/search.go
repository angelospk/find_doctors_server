package aggregator

import (
	"context"
	"fmt"
	"log"
	"sort"
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

// Aggregator coordinates concurrent searches across different entities.
type Aggregator struct {
	client     MinistryClient
	specCache  []ministry.Specialty
	specMu     sync.RWMutex
	lastSpecUp time.Time
}

// New creates a new Aggregator instance.
func New(client MinistryClient) *Aggregator {
	return &Aggregator{client: client}
}

// SearchUnified runs searches across Hospitals (1) and PFY (18) concurrently and merges results.
func (a *Aggregator) SearchUnified(ctx context.Context, payload ministry.SearchPayload) ([]ministry.HUnit, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	allUnits := []ministry.HUnit{}
	var errs []error

	foreasIDs := []int{1, 18}

	for _, fID := range foreasIDs {
		wg.Add(1)
		// Launch a goroutine for each foreasID
		go func(id int) {
			defer wg.Done()
			
			// Copy the payload and inject the specific ForeasID
			p := payload
			p.ForeasID = id
			
			units, err := a.client.SearchHUnits(ctx, p)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fmt.Printf("Error searching Foreas %d: %v\n", id, err)
				errs = append(errs, err)
				return
			}
			fmt.Printf("Found %d units for Foreas %d\n", len(units), id)
			allUnits = append(allUnits, units...)
		}(fID)
	}

	wg.Wait()
	fmt.Printf("Total units found after merge: %d\n", len(allUnits))

	// If all requests failed, return the first error
	if len(errs) == len(foreasIDs) {
		return nil, errs[0]
	}

	return allUnits, nil
}

// ScannedUnit represents a health unit with its earliest available appointment date.
// tygo:generate
type ScannedUnit struct {
	ministry.HUnit
	FirstDate *string `json:"firstDate"`
	ScanOK    bool    `json:"scanOk"` // false if the upstream availability check failed
}

// FastScanner concurrently checks the first available slot for a list of units.
// It uses a semaphore pattern to prevent overwhelming the upstream API.
func (a *Aggregator) FastScanner(ctx context.Context, units []ministry.HUnit, payload ministry.SearchPayload) []ScannedUnit {
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := []ScannedUnit{}

	// Limit concurrent requests to 20
	sem := make(chan struct{}, 20)

	for _, u := range units {
		// Only check if we have a valid unit ID
		if u.HUnit == nil {
			continue
		}

		wg.Add(1)
		go func(unit ministry.HUnit) {
			defer wg.Done()
			
			sem <- struct{}{}        // Acquire token
			defer func() { <-sem }() // Release token

			p := payload
			p.HUnit = unit.HUnit
			p.ForeasID = unit.ForeasID

			dateStr, err := a.client.FirstAvailableSlot(ctx, p)
			scanOK := err == nil
			var datePtr *string
			if err == nil && len(dateStr) == 10 {
				datePtr = &dateStr
			}

			fmt.Printf("Unit %d (%s) result: %v\n", *unit.HUnit, unit.Name, datePtr)
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
// It prioritizes "Soonest" (Date) then "Closest" (Distance) within a specific radius.
func (a *Aggregator) SmartSearch(ctx context.Context, units []ministry.HUnit, payload ministry.SearchPayload, opts SmartSearchOptions) []ScannedUnit {
	// 1. Scan availability for all candidates
	scanned := a.FastScanner(ctx, units, payload)

	// 2. Filter by distance if coordinates and max distance provided
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

	// 3. Multi-level Sorting:
	// Primary: FirstAvailableDate ASC
	// Secondary: Distance from user ASC
	sort.Slice(filtered, func(i, j int) bool {
		// Handle nil dates: push to the end
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
		// Sub-sort by distance if coordinates are available
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

// GetGranularSlots fetches and flattens all available appointment times for a unit on a given date.
func (a *Aggregator) GetGranularSlots(ctx context.Context, hunitID, foreasID int, prefID *int, specialtyID int, date string) ([]GranularSlot, error) {
	parsedDate, err := time.Parse(time.RFC3339, date)
	if err != nil {
		// Try simple date format
		parsedDate, err = time.Parse("2006-01-02", date)
		if err != nil {
			return nil, fmt.Errorf("invalid date format: %w", err)
		}
	}

	startDay := parsedDate.Truncate(24 * time.Hour)
	endDay := startDay.Add(23*time.Hour + 59*time.Minute + 59*time.Second)

	// 1. Get capacity chunks (groups) for the day
	payload := ministry.SlotsInitPayload{
		StartDate:    startDay.Format("2006-01-02T15:04:05.000Z"),
		EndDate:      endDay.Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specialtyID,
		PrefectureID: prefID,
		HUnit:        &hunitID,
		ForeasID:     foreasID,
		IsCovid:      0,
		IsOnlyFd:     0,
		IsMachine:    0,
	}

	groups, err := a.client.GetSlotsInit(ctx, payload)
	if err != nil {
		return nil, err
	}

	// 2. Concurrently fetch actual slots for each group that has capacity
	type result struct {
		groupID int
		slots   []ministry.ActualSlot
		err     error
	}

	resChan := make(chan result, 12)
	var wg sync.WaitGroup

	for _, g := range groups {
		// 1. Filter by requested day (only if API provides it, e.g. for PFY 7-day calendars)
		if g.Day > 0 && g.Day != int(parsedDate.Weekday()) {
			continue
		}

		// 2. Skip empty or invalid groups
		if g.GroupColor == "" || g.GroupColor == "disabled" {
			continue
		}

		day := g.Day
		wg.Add(1)
		go func(groupID int) {
			defer wg.Done()
			
			// Prefecture ID is required for getactualslots or it returns []
			pID := 0
			if prefID != nil {
				pID = *prefID
			}

			p := ministry.GetActualSlotsPayload{
				Day:          day,
				DDate:        startDay.Format("2006-01-02T15:04:05.000Z"),
				GroupID:      groupID,
				HUnit:        hunitID,
				Foreas:       foreasID,
				SpecialityID: specialtyID,
				PrefectureID: pID,
				IsOnlyFd:     0,
				IsMachine:    0,
				CDoorID:      nil,
			}
			slots, err := a.client.GetActualSlots(ctx, p)
			resChan <- result{groupID: groupID, slots: slots, err: err}
		}(g.GroupID)
	}

	wg.Wait()
	close(resChan)

	// 3. Flatten and unify results
	var allSlots []GranularSlot
	for r := range resChan {
		if r.err != nil {
			log.Printf("Warning: failed to fetch actual slots for group %d: %v", r.groupID, r.err)
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

	// 4. Sort by time
	sort.Slice(allSlots, func(i, j int) bool {
		return allSlots[i].Time < allSlots[j].Time
	})

	return allSlots, nil
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
	ID        int     `json:"specialtyId"`
	Name      string  `json:"name"`
	FillRate  float64 `json:"fillRate"`  // Percentage of "disabled" slots
	FirstDate *string `json:"firstDate"` // Earliest available slot
}

// CapacityReport represents the overall capacity for a medical unit.
// tygo:generate
type CapacityReport struct {
	HUnitID     int                 `json:"hunitId"`
	Scanned     int                 `json:"scanned"`
	Specialties []SpecialtyCapacity `json:"specialties"`
}

// HospitalCapacity aggregates capacity across all specialties for a unit.
// Window is hardcoded to the current week (Monday to Sunday).
func (a *Aggregator) HospitalCapacity(ctx context.Context, hunitID, foreasID int, prefID *int, specialties []ministry.Specialty) (CapacityReport, error) {
	now := time.Now().UTC()
	// Next Monday to next Sunday
	offset := 8 - int(now.Weekday())
	if offset > 7 {
		offset = 1
	}
	monday := now.AddDate(0, 0, offset).Truncate(24 * time.Hour)
	sunday := monday.AddDate(0, 0, 6)

	startStr := monday.Format("2006-01-02T15:04:05.000Z")
	endStr := sunday.Format("2006-01-02T15:04:05.000Z")

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
	sem := make(chan struct{}, 30) // Semaphore cap

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

			// Also fetch the first available slot date for this specialty to make the heatmap actionable
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
				log.Printf("Warning: failed to check first slot for unit %d specialty %d: %v", hunitID, s.ID, err)
			}
			var datePtr *string
			if len(firstDate) == 10 {
				datePtr = &firstDate
			}

			if len(groups) == 0 {
				resChan <- result{
					spec: s,
					cap: SpecialtyCapacity{
						ID:        s.ID,
						Name:      s.Name,
						FillRate:  0.0,
						FirstDate: datePtr,
					},
				}
				return
			}

			// Handle responseCode: 2 (No appointments found)
			if len(groups) == 1 && groups[0].ResponseCode == 2 {
				resChan <- result{
					spec: s,
					cap: SpecialtyCapacity{
						ID:        s.ID,
						Name:      s.Name,
						FillRate:  0.0, // Treat as available (0% full)
						FirstDate: datePtr,
					},
				}
				return
			}

			disabled := 0
			total := 0
			for _, g := range groups {
				if g.GroupColor == "" {
					continue // Meta info / error object
				}
				total++
				if g.GroupColor == "disabled" {
					disabled++
				}
			}

			if total == 0 {
				resChan <- result{
					spec: s,
					cap: SpecialtyCapacity{
						ID:        s.ID,
						Name:      s.Name,
						FillRate:  0.0,
						FirstDate: datePtr,
					},
				}
				return
			}

			fillRate := (float64(disabled) / float64(total)) * 100.0
			resChan <- result{
				spec: s,
				cap: SpecialtyCapacity{
					ID:        s.ID,
					Name:      s.Name,
					FillRate:  fillRate,
					FirstDate: datePtr,
				},
			}
		}(spec)
	}

	wg.Wait()
	close(resChan)

	for r := range resChan {
		if r.err != nil {
			log.Printf("Error scanning specialty %s (%d): %v", r.spec.Name, r.spec.ID, r.err)
			continue
		}
		if r.cap.ID != 0 {
			report.Specialties = append(report.Specialties, r.cap)
		}
	}

	report.Scanned = len(report.Specialties)
	
	// Sort by fill rate descending
	sort.Slice(report.Specialties, func(i, j int) bool {
		return report.Specialties[i].FillRate > report.Specialties[j].FillRate
	})

	return report, nil
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

	// Double-check after lock
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
