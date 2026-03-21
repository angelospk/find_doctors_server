package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
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

// HandleNationwideSearch acts as a proxy to find all available units across the country.
// Example: GET /api/nationwide?specialtyId=6
func (s *Server) HandleNationwideSearch(w http.ResponseWriter, r *http.Request) {
	specialityIDStr := r.URL.Query().Get("specialtyId")
	specialityID, err := strconv.Atoi(specialityIDStr)
	if err != nil {
		http.Error(w, "invalid or missing specialtyId", http.StatusBadRequest)
		return
	}

	startDate := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	endDate := time.Now().AddDate(0, 6, 0).UTC().Format("2006-01-02T15:04:05.000Z")

	var prefPtr *int
	if prefStr := r.URL.Query().Get("prefectureId"); prefStr != "" {
		if id, err := strconv.Atoi(prefStr); err == nil {
			prefPtr = &id
		}
	}

	payload := ministry.SearchPayload{
		StartDate:    startDate,
		EndDate:      endDate,
		SpecialityID: specialityID,
		PrefectureID: prefPtr,
		IsCovid:      0,
		IsOnlyFd:     0,
		IsMachine:    0,
	}

	ctx := context.Background()
	// 1. Concurrent Cross-Entity Search (Foreas 1 + 18)
	units, err := s.agg.SearchUnified(ctx, payload)
	if err != nil {
		http.Error(w, "failed to search health units: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 2. Concurrent Fast Scanner filtering
	scanned := s.agg.FastScanner(ctx, units, payload)

	response := SearchResponse{
		Count:   len(scanned),
		Results: scanned,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleEmergency finds the closest available appointments for a specialty.
// Example: GET /api/emergency?specialtyId=6&lat=37.9838&lon=23.7275
func (s *Server) HandleEmergency(w http.ResponseWriter, r *http.Request) {
	specialityIDStr := r.URL.Query().Get("specialtyId")
	specialityID, _ := strconv.Atoi(specialityIDStr)
	latStr := r.URL.Query().Get("lat")
	lonStr := r.URL.Query().Get("lon")
	userLat, _ := strconv.ParseFloat(latStr, 64)
	userLon, _ := strconv.ParseFloat(lonStr, 64)

	if specialityID == 0 {
		http.Error(w, "missing specialtyId", http.StatusBadRequest)
		return
	}

	payload := ministry.SearchPayload{
		StartDate:    time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		EndDate:      time.Now().AddDate(0, 1, 0).UTC().Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specialityID,
		PrefectureID: nil, // Search nationwide to find "closest" regardless of prefecture
	}

	units, err := s.agg.SearchUnified(context.Background(), payload)
	if err != nil {
		http.Error(w, "failed to search units", http.StatusInternalServerError)
		return
	}

	// Calculate distances if lat/lon provided
	if userLat != 0 && userLon != 0 {
		sort.Slice(units, func(i, j int) bool {
			return distance(userLat, userLon, units[i].Latitude, units[i].Longitude) <
				distance(userLat, userLon, units[j].Latitude, units[j].Longitude)
		})
	}

	// Limit to top 10 closest units for the "Fast Scanner" to avoid excessive API calls
	if len(units) > 10 {
		units = units[:10]
	}

	scanned := s.agg.FastScanner(context.Background(), units, payload)

	// Sort by date then distance
	sort.Slice(scanned, func(i, j int) bool {
		if scanned[i].FirstDate != scanned[j].FirstDate {
			return scanned[i].FirstDate < scanned[j].FirstDate
		}
		return distance(userLat, userLon, scanned[i].Latitude, scanned[i].Longitude) <
			distance(userLat, userLon, scanned[j].Latitude, scanned[j].Longitude)
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(scanned)
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

// distance calculates the Haversine distance between two points in km.
func distance(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371 // Earth radius in km
	dLat := (lat2 - lat1) * (math.Pi / 180)
	dLon := (lon2 - lon1) * (math.Pi / 180)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*(math.Pi/180))*math.Cos(lat2*(math.Pi/180))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}
