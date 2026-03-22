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
	FirstDate string `json:"firstDate"`
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
			if err != nil || len(dateStr) != 10 {
				fmt.Printf("Unit %d (%s) result: error/invalid (%v / %s)\n", *unit.HUnit, unit.Name, err, dateStr)
				// Ignore errors or invalid date formats (like HTML error pages or empty "")
				return
			}

			fmt.Printf("Unit %d (%s) result: %s\n", *unit.HUnit, unit.Name, dateStr)
			mu.Lock()
			results = append(results, ScannedUnit{
				HUnit:     unit,
				FirstDate: dateStr,
			})
			mu.Unlock()
		}(u)
	}

	wg.Wait()
	return results
}

// SpecialtyCapacity represents the calculated fill-rate for a single specialty.
// tygo:generate
type SpecialtyCapacity struct {
	ID       int     `json:"specialityId"`
	Name     string  `json:"name"`
	FillRate float64 `json:"fillRate"` // Percentage of "disabled" slots
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

			if len(groups) == 0 {
				resChan <- result{spec: s, err: nil} // No slots returned, skip
				return
			}

			// Handle responseCode: 2 (No appointments found)
			if len(groups) == 1 && groups[0].ResponseCode == 2 {
				resChan <- result{
					spec: s,
					cap: SpecialtyCapacity{
						ID:       s.ID,
						Name:     s.Name,
						FillRate: 0.0, // Treat as available (0% full)
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
				resChan <- result{spec: s, err: nil}
				return
			}

			fillRate := (float64(disabled) / float64(total)) * 100.0
			resChan <- result{
				spec: s,
				cap: SpecialtyCapacity{
					ID:       s.ID,
					Name:     s.Name,
					FillRate: fillRate,
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
