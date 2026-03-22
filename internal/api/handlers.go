package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// Server represents our REST API HTTP server.
type Server struct {
	agg *aggregator.Aggregator
}

// NewServer initializes a new Server instance.
func NewServer(agg *aggregator.Aggregator) *Server {
	return &Server{agg: agg}
}

// SearchResponse represents the response sent back to the frontend.
type SearchResponse struct {
	Count   int                      `json:"count"`
	Results []aggregator.ScannedUnit `json:"results"`
}

// HandleSmartSearch handles the unified prioritized search.
// GET /api/search?specialtyId=6&lat=37.9&lon=23.7&maxDistanceInKm=200
func (s *Server) HandleSmartSearch(w http.ResponseWriter, r *http.Request) {
	specIDStr := r.URL.Query().Get("specialtyId")
	if specIDStr == "" {
		http.Error(w, "missing specialtyId", http.StatusBadRequest)
		return
	}
	specialtyID, err := strconv.Atoi(specIDStr)
	if err != nil {
		http.Error(w, "invalid specialtyId: must be an integer", http.StatusBadRequest)
		return
	}

	var lat, lon *float64
	if latStr := r.URL.Query().Get("lat"); latStr != "" {
		if val, err := strconv.ParseFloat(latStr, 64); err == nil {
			lat = &val
		}
	}
	if lonStr := r.URL.Query().Get("lon"); lonStr != "" {
		if val, err := strconv.ParseFloat(lonStr, 64); err == nil {
			lon = &val
		}
	}
	maxDist, _ := strconv.ParseFloat(r.URL.Query().Get("maxDistanceInKm"), 64)

	var prefPtr *int
	if prefStr := r.URL.Query().Get("prefectureId"); prefStr != "" {
		if id, err := strconv.Atoi(prefStr); err == nil {
			prefPtr = &id
		}
	}

	payload := ministry.SearchPayload{
		StartDate:    time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		EndDate:      time.Now().AddDate(0, 6, 0).UTC().Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specialtyID,
		PrefectureID: prefPtr,
	}

	ctx := r.Context()
	// 1. Initial candidates discovery
	units, err := s.agg.SearchUnified(ctx, payload)
	if err != nil {
		http.Error(w, "search failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 2. Prioritized scanning and sorting
	results := s.agg.SmartSearch(ctx, units, payload, aggregator.SmartSearchOptions{
		Lat:         lat,
		Lon:         lon,
		MaxDistance: maxDist,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// HandleGetSpecialties returns the cached list of medical specialties.
func (s *Server) HandleGetSpecialties(w http.ResponseWriter, r *http.Request) {
	specs, err := s.agg.GetSpecialties(context.Background())
	if err != nil {
		http.Error(w, "failed to fetch specialties", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(specs)
}

// HandleHospitalCapacity returns a weekly capacity report for a specific hospital.
// Example: GET /api/hospitals/70600/capacity
func (s *Server) HandleHospitalCapacity(w http.ResponseWriter, r *http.Request) {
	hunitIDStr := r.PathValue("hunitId")
	if hunitIDStr == "" {
		http.Error(w, "missing hunitId in path", http.StatusBadRequest)
		return
	}

	hunitID, err := strconv.Atoi(hunitIDStr)
	if err != nil {
		http.Error(w, "invalid hunitId in path", http.StatusBadRequest)
		return
	}

	// We'll also need foreasId, default to 1 (Hospitals)
	foreasID := 1
	if fStr := r.URL.Query().Get("foreasId"); fStr != "" {
		if id, err := strconv.Atoi(fStr); err == nil {
			foreasID = id
		}
	}

	var prefPtr *int
	if pStr := r.URL.Query().Get("prefectureId"); pStr != "" {
		if id, err := strconv.Atoi(pStr); err == nil {
			prefPtr = &id
		}
	}

	// 1. Get specialties (to fan out)
	specs, err := s.agg.GetSpecialties(context.Background())
	if err != nil {
		http.Error(w, "failed to fetch specialties", http.StatusInternalServerError)
		return
	}

	// Filter specialties if specific ID provided
	var filteredSpecs []ministry.Specialty
	specIDQuery := r.URL.Query().Get("specialtyId")
	if specIDQuery != "" {
		targetID, err := strconv.Atoi(specIDQuery)
		if err != nil {
			http.Error(w, "invalid specialtyId: must be an integer", http.StatusBadRequest)
			return
		}
		for _, spec := range specs {
			if spec.ID == targetID {
				filteredSpecs = append(filteredSpecs, spec)
				break
			}
		}
		if len(filteredSpecs) == 0 {
			http.Error(w, "unknown specialtyId", http.StatusNotFound)
			return
		}
	} else {
		filteredSpecs = specs
	}

	// 2. Aggregate capacity
	report, err := s.agg.HospitalCapacity(context.Background(), hunitID, foreasID, prefPtr, filteredSpecs)
	if err != nil {
		http.Error(w, "failed to generate capacity report: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

