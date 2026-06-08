package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// Server represents our REST API HTTP server.
type Server struct {
	agg    *aggregator.Aggregator
	logger *slog.Logger
}

// NewServer initializes a new Server instance.
func NewServer(agg *aggregator.Aggregator) *Server {
	return &Server{agg: agg, logger: slog.Default()}
}

// WithLogger attaches a slog logger and returns the server.
func (s *Server) WithLogger(l *slog.Logger) *Server {
	if l != nil {
		s.logger = l
	}
	return s
}

// SearchResponse represents the response sent back to the frontend.
type SearchResponse struct {
	Count   int                      `json:"count"`
	Results []aggregator.ScannedUnit `json:"results"`
}

func parseIntPtr(s string) *int {
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &v
}

func parseFloatPtr(s string) *float64 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

// HandleSmartSearch handles the unified prioritized search.
// GET /api/search?specialtyId=6&lat=37.9&lon=23.7&maxDistanceInKm=200
func (s *Server) HandleSmartSearch(w http.ResponseWriter, r *http.Request) {
	specIDStr := r.URL.Query().Get("specialtyId")
	if specIDStr == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "missing specialtyId")
		return
	}
	specialtyID, err := strconv.Atoi(specIDStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId must be an integer")
		return
	}

	lat := parseFloatPtr(r.URL.Query().Get("lat"))
	lon := parseFloatPtr(r.URL.Query().Get("lon"))
	maxDist, _ := strconv.ParseFloat(r.URL.Query().Get("maxDistanceInKm"), 64)
	prefPtr := parseIntPtr(r.URL.Query().Get("prefectureId"))

	payload := ministry.SearchPayload{
		StartDate:    time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		EndDate:      time.Now().AddDate(0, 6, 0).UTC().Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specialtyID,
		PrefectureID: prefPtr,
	}

	ctx := r.Context()
	units, err := s.agg.SearchUnified(ctx, payload)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "search failed: "+err.Error())
		return
	}

	results := s.agg.SmartSearch(ctx, units, payload, aggregator.SmartSearchOptions{
		Lat:         lat,
		Lon:         lon,
		MaxDistance: maxDist,
	})

	writeJSON(w, http.StatusOK, results)
}

// HandleGetSpecialties returns the cached list of medical specialties.
func (s *Server) HandleGetSpecialties(w http.ResponseWriter, r *http.Request) {
	specs, err := s.agg.GetSpecialties(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "failed to fetch specialties: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, specs)
}

// HandleHospitalCapacity returns a weekly capacity report for a specific hospital.
func (s *Server) HandleHospitalCapacity(w http.ResponseWriter, r *http.Request) {
	hunitIDStr := r.PathValue("hunitId")
	if hunitIDStr == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "missing hunitId in path")
		return
	}
	hunitID, err := strconv.Atoi(hunitIDStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "hunitId must be an integer")
		return
	}

	foreasID := 1
	if fStr := r.URL.Query().Get("foreasId"); fStr != "" {
		if id, err := strconv.Atoi(fStr); err == nil {
			foreasID = id
		}
	}
	prefPtr := parseIntPtr(r.URL.Query().Get("prefectureId"))

	specs, err := s.agg.GetSpecialties(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "failed to fetch specialties: "+err.Error())
		return
	}

	var filteredSpecs []ministry.Specialty
	specIDQuery := r.URL.Query().Get("specialtyId")
	if specIDQuery != "" {
		targetID, err := strconv.Atoi(specIDQuery)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId must be an integer")
			return
		}
		for _, spec := range specs {
			if spec.ID == targetID {
				filteredSpecs = append(filteredSpecs, spec)
				break
			}
		}
		if len(filteredSpecs) == 0 {
			writeJSONError(w, http.StatusNotFound, "not_found", "unknown specialtyId")
			return
		}
	} else {
		filteredSpecs = specs
	}

	report, err := s.agg.HospitalCapacity(r.Context(), hunitID, foreasID, prefPtr, filteredSpecs)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "failed to generate capacity report: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// HandleGranularSlots returns detailed appointment slots for a specific unit and date.
func (s *Server) HandleGranularSlots(w http.ResponseWriter, r *http.Request) {
	hunitIDStr := r.PathValue("hunitId")
	if hunitIDStr == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "missing hunitId in path")
		return
	}
	hunitID, err := strconv.Atoi(hunitIDStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "hunitId must be an integer")
		return
	}

	specIDStr := r.URL.Query().Get("specialtyId")
	if specIDStr == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "missing specialtyId in query")
		return
	}
	specialtyID, err := strconv.Atoi(specIDStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId must be an integer")
		return
	}

	date := r.URL.Query().Get("date")
	if date == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "missing date in query")
		return
	}

	foreasID := 1
	if fStr := r.URL.Query().Get("foreasId"); fStr != "" {
		if id, err := strconv.Atoi(fStr); err == nil {
			foreasID = id
		}
	}
	prefPtr := parseIntPtr(r.URL.Query().Get("prefectureId"))
	cDoorPtr := parseIntPtr(r.URL.Query().Get("cDoorId"))

	timeOfDay := r.URL.Query().Get("timeOfDay")
	switch timeOfDay {
	case "", "morning", "noon", "afternoon":
		// allowed
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "timeOfDay must be one of: morning, noon, afternoon")
		return
	}

	slots, err := s.agg.GetGranularSlots(r.Context(), hunitID, foreasID, prefPtr, specialtyID, date, aggregator.GranularSlotsOptions{
		CDoorID:   cDoorPtr,
		TimeOfDay: timeOfDay,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "failed to fetch granular slots: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, slots)
}

// HandleHealthz is a fast liveness probe; no upstream dependency.
func (s *Server) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleReadyz performs a live, cache-bypassing upstream reachability check so
// readiness reflects the ministry API's true state rather than a stale 24h cache.
func (s *Server) HandleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.agg.Ready(ctx); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "upstream_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// HandleHealthUnitTypes returns the foreas/health-unit-types catalog (#17).
func (s *Server) HandleHealthUnitTypes(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	types, err := dc.GetHealthUnitTypes(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types)
}

// HandlePrefectures returns the standard prefecture catalog (#18).
func (s *Server) HandlePrefectures(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	prefs, err := dc.GetPrefectures(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// HandleCovidPrefectures returns prefectures with COVID vaccination centers.
func (s *Server) HandleCovidPrefectures(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	prefs, err := dc.GetCovidPrefectures(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// HandleMentalHealthPrefectures returns prefectures with mental health units.
func (s *Server) HandleMentalHealthPrefectures(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	prefs, err := dc.GetMentalHealthPrefectures(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// HandleDoctorSearch wraps /rv/searchdoctors (#11, #19).
func (s *Server) HandleDoctorSearch(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	specID, err := strconv.Atoi(r.URL.Query().Get("specialtyId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId required")
		return
	}
	foreasID := 19 // default to ΕΟΠΥΥ private doctors
	if fStr := r.URL.Query().Get("foreasId"); fStr != "" {
		if id, err := strconv.Atoi(fStr); err == nil {
			foreasID = id
		}
	}
	now := time.Now().UTC()
	payload := ministry.SearchDoctorsPayload{
		StartDate:    now.Format("2006-01-02T15:04:05.000Z"),
		EndDate:      now.AddDate(0, 6, 0).Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specID,
		ForeasID:     foreasID,
		PrefectureID: parseIntPtr(r.URL.Query().Get("prefectureId")),
		FirstName:    r.URL.Query().Get("firstName"),
		LastName:     r.URL.Query().Get("lastName"),
	}
	docs, err := dc.SearchDoctors(r.Context(), payload)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, docs)
}

// HandleDoctorNearby wraps /rv/searchdoctors/currentlocation (#19).
func (s *Server) HandleDoctorNearby(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	specID, err := strconv.Atoi(r.URL.Query().Get("specialtyId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId required")
		return
	}
	lat := parseFloatPtr(r.URL.Query().Get("lat"))
	lon := parseFloatPtr(r.URL.Query().Get("lon"))
	if lat == nil || lon == nil {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "lat and lon are required")
		return
	}
	dist := parseFloatPtr(r.URL.Query().Get("distance"))
	foreasID := 19
	if fStr := r.URL.Query().Get("foreasId"); fStr != "" {
		if id, err := strconv.Atoi(fStr); err == nil {
			foreasID = id
		}
	}
	now := time.Now().UTC()
	payload := ministry.SearchDoctorsPayload{
		StartDate:    now.Format("2006-01-02T15:04:05.000Z"),
		EndDate:      now.AddDate(0, 6, 0).Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specID,
		ForeasID:     foreasID,
		Latitude:     lat,
		Longitude:    lon,
		Distance:     dist,
	}
	docs, err := dc.SearchDoctorsByLocation(r.Context(), payload)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, docs)
}

// HandleFamilyDoctorSearch wraps /rv/searchdoctorsfd and /rv/searchhunitsfd (#12).
// Returns a hybrid {units,doctors} envelope.
func (s *Server) HandleFamilyDoctorSearch(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	specID, err := strconv.Atoi(r.URL.Query().Get("specialtyId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId required")
		return
	}
	prefPtr := parseIntPtr(r.URL.Query().Get("prefectureId"))

	now := time.Now().UTC()
	start := now.Format("2006-01-02T15:04:05.000Z")
	end := now.AddDate(0, 6, 0).Format("2006-01-02T15:04:05.000Z")

	docsPayload := ministry.SearchDoctorsPayload{
		StartDate:    start,
		EndDate:      end,
		SpecialityID: specID,
		ForeasID:     18,
		PrefectureID: prefPtr,
		IsOnlyFd:     1,
	}
	unitsPayload := ministry.SearchPayload{
		StartDate:    start,
		EndDate:      end,
		SpecialityID: specID,
		ForeasID:     18,
		PrefectureID: prefPtr,
		IsOnlyFd:     1,
	}

	docs, dErr := dc.SearchDoctorsFD(r.Context(), docsPayload)
	units, uErr := dc.SearchHunitsFD(r.Context(), unitsPayload)
	if dErr != nil && uErr != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", dErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"doctors": docs, "units": units})
}

// HandleCovidSearch wraps /rv/searchhunits with isCovid=1 (#14).
func (s *Server) HandleCovidSearch(w http.ResponseWriter, r *http.Request) {
	specIDStr := r.URL.Query().Get("specialtyId")
	if specIDStr == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "specialtyId required")
		return
	}
	specID, err := strconv.Atoi(specIDStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId must be an integer")
		return
	}
	prefPtr := parseIntPtr(r.URL.Query().Get("prefectureId"))
	foreasID := 1
	if fStr := r.URL.Query().Get("foreasId"); fStr != "" {
		if id, err := strconv.Atoi(fStr); err == nil {
			foreasID = id
		}
	}
	now := time.Now().UTC()
	payload := ministry.SearchPayload{
		StartDate:    now.Format("2006-01-02T15:04:05.000Z"),
		EndDate:      now.AddDate(0, 6, 0).Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specID,
		PrefectureID: prefPtr,
		ForeasID:     foreasID,
		IsCovid:      1,
	}
	units, err := s.agg.Client().SearchHUnits(r.Context(), payload)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, units)
}

// HandleMentalHealthSearch sets rvtypeId=15 + isMentalHealth=1 (#13).
func (s *Server) HandleMentalHealthSearch(w http.ResponseWriter, r *http.Request) {
	specIDStr := r.URL.Query().Get("specialtyId")
	if specIDStr == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "specialtyId required")
		return
	}
	specID, err := strconv.Atoi(specIDStr)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId must be an integer")
		return
	}
	prefPtr := parseIntPtr(r.URL.Query().Get("prefectureId"))
	foreasID := 1
	if fStr := r.URL.Query().Get("foreasId"); fStr != "" {
		if id, err := strconv.Atoi(fStr); err == nil {
			foreasID = id
		}
	}
	now := time.Now().UTC()
	payload := ministry.SearchPayload{
		StartDate:      now.Format("2006-01-02T15:04:05.000Z"),
		EndDate:        now.AddDate(0, 6, 0).Format("2006-01-02T15:04:05.000Z"),
		SpecialityID:   specID,
		PrefectureID:   prefPtr,
		ForeasID:       foreasID,
		IsMentalHealth: 1,
		RvTypeID:       15,
	}
	units, err := s.agg.Client().SearchHUnits(r.Context(), payload)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, units)
}

// HandleMachineRvTypes returns the diagnostic-machine RV type catalog (#15).
func (s *Server) HandleMachineRvTypes(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	types, err := dc.GetMachineRvTypes(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, types)
}

// HandleMachineSearch wraps /machines/searchHunitsMachines (#15).
func (s *Server) HandleMachineSearch(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	rvTypeID, err := strconv.Atoi(r.URL.Query().Get("rvTypeId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "rvTypeId required")
		return
	}
	prefPtr := parseIntPtr(r.URL.Query().Get("prefectureId"))
	now := time.Now().UTC()
	payload := ministry.SearchPayload{
		StartDate:    now.Format("2006-01-02T15:04:05.000Z"),
		EndDate:      now.AddDate(0, 6, 0).Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: 0,
		PrefectureID: prefPtr,
		ForeasID:     1,
		RvTypeID:     rvTypeID,
		IsMachine:    1,
	}
	units, err := dc.SearchHunitsMachines(r.Context(), payload)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"disclaimer": "These results require paid examinations (payType=1).",
		"units":      units,
	})
}

// HandleClinicDoors returns the clinic-door breakdown for a hospital+specialty (#20).
func (s *Server) HandleClinicDoors(w http.ResponseWriter, r *http.Request) {
	dc := s.agg.DoctorClient()
	if dc == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "extended ministry client not wired")
		return
	}
	hunitID, err := strconv.Atoi(r.PathValue("hunitId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "hunitId must be an integer")
		return
	}
	specID, err := strconv.Atoi(r.URL.Query().Get("specialtyId"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId required")
		return
	}
	doors, err := dc.GetClinicDoors(r.Context(), hunitID, specID)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, doors)
}
