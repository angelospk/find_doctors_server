package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/ministry"
	"github.com/angelospk/find_doctors_server/internal/watch"
)

// upstreamHealthTTL controls how long /healthz/upstream caches a successful
// probe. Failures are not cached so recovery is observed immediately.
const upstreamHealthTTL = 30 * time.Second

// Server represents our REST API HTTP server.
type Server struct {
	agg    *aggregator.Aggregator
	logger *slog.Logger

	upHealthMu sync.Mutex
	upHealthOK bool
	upHealthAt time.Time

	// Cancellation watchdog (#5). nil when the feature isn't wired.
	watches     *watch.Store
	watchSeeder watch.SlotChecker
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

// requireInt extracts a required integer query parameter. If absent or unparseable,
// it writes a structured JSON 400 to w and returns ok=false. Callers should
// return immediately when ok is false.
func requireInt(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	s := r.URL.Query().Get(name)
	if s == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "missing "+name)
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", name+" must be an integer")
		return 0, false
	}
	return v, true
}

// pagination returns (limit, offset) clamped to safe bounds, defaulting to
// limit=50, offset=0. limit is capped at 200 to keep response sizes reasonable.
func pagination(r *http.Request) (limit, offset int) {
	limit = 50
	offset = 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	return limit, offset
}

func paginate[T any](xs []T, limit, offset int) []T {
	if offset >= len(xs) {
		return []T{}
	}
	end := offset + limit
	if end > len(xs) {
		end = len(xs)
	}
	return xs[offset:end]
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

	// Optional date window (fromDate/toDate, YYYY-MM-DD) scopes firstavailableslot
	// so firstDate reflects the caller's desired dates. EndDate is the exclusive
	// next-day boundary of toDate (avoids dropping late slots via 23:59:59 drift).
	if fromStr := r.URL.Query().Get("fromDate"); fromStr != "" {
		from, derr := aggregator.ParseFlexibleDate(fromStr)
		if derr != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_param", "fromDate must be YYYY-MM-DD or RFC3339")
			return
		}
		payload.StartDate = from.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	if toStr := r.URL.Query().Get("toDate"); toStr != "" {
		to, derr := aggregator.ParseFlexibleDate(toStr)
		if derr != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_param", "toDate must be YYYY-MM-DD or RFC3339")
			return
		}
		payload.EndDate = to.AddDate(0, 0, 1).UTC().Format("2006-01-02T15:04:05.000Z")
	}

	ctx := r.Context()
	units, err := s.agg.SearchUnified(ctx, payload)
	if err != nil {
		s.logger.Warn("smart search upstream failed", "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
		return
	}

	results := s.agg.SmartSearch(ctx, units, payload, aggregator.SmartSearchOptions{
		Lat:         lat,
		Lon:         lon,
		MaxDistance: maxDist,
	})

	limit, offset := pagination(r)
	page := paginate(results, limit, offset)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": page,
		"meta": map[string]any{"total": len(results), "limit": limit, "offset": offset},
	})
}

// HandleGetSpecialties returns the cached list of medical specialties.
func (s *Server) HandleGetSpecialties(w http.ResponseWriter, r *http.Request) {
	specs, err := s.agg.GetSpecialties(r.Context())
	if err != nil {
		s.logger.Warn("specialties fetch failed", "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "failed to fetch specialties")
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
		s.logger.Warn("specialties fetch failed", "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "failed to fetch specialties")
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
		s.logger.Warn("capacity report failed", "hunit", hunitID, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "failed to generate capacity report")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// HandleNationwideHeatmap returns a prefecture-level fill-rate heatmap for a given specialty.
// GET /api/heatmap?specialtyId=X
func (s *Server) HandleNationwideHeatmap(w http.ResponseWriter, r *http.Request) {
	specialtyID, ok := requireInt(w, r, "specialtyId")
	if !ok {
		return
	}

	report, err := s.agg.NationwideHeatmap(r.Context(), specialtyID)
	if err != nil {
		s.logger.Warn("heatmap generation failed", "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "heatmap generation failed")
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
	if _, derr := aggregator.ParseFlexibleDate(date); derr != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "date must be YYYY-MM-DD or RFC3339")
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
		s.logger.Warn("granular slots failed", "hunit", hunitID, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "failed to fetch granular slots")
		return
	}
	if slots == nil {
		slots = []aggregator.GranularSlot{}
	}
	writeJSON(w, http.StatusOK, slots)
}

// HandleHealthz is a fast liveness probe; no upstream dependency.
func (s *Server) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleReadyz reports whether the process is ready to serve traffic. It does
// NOT depend on the Ministry API being reachable — that's intentional. K8s
// readiness should not flap when an external dependency hiccups; clients
// learn about upstream failures from the per-request 502s. Use
// /healthz/upstream to surface Ministry reachability separately.
func (s *Server) HandleReadyz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// HandleUpstreamHealth reports Ministry API reachability with a short TTL
// cache so admin tooling can poll without amplifying load. Probes use a 3s
// timeout so a wedged upstream doesn't hang the check.
func (s *Server) HandleUpstreamHealth(w http.ResponseWriter, r *http.Request) {
	s.upHealthMu.Lock()
	cached := s.upHealthOK
	age := time.Since(s.upHealthAt)
	s.upHealthMu.Unlock()
	if cached && age < upstreamHealthTTL {
		writeJSON(w, http.StatusOK, map[string]any{
			"upstream":   "up",
			"checked_at": s.upHealthAt.UTC().Format(time.RFC3339),
			"cached":     true,
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	// Use the cache-bypassing Ready() probe (not the 24h-cached GetSpecialties)
	// so /healthz/upstream reflects the ministry API's true reachability.
	if err := s.agg.Ready(ctx); err != nil {
		s.upHealthMu.Lock()
		s.upHealthOK = false
		s.upHealthAt = time.Now()
		s.upHealthMu.Unlock()
		writeJSONError(w, http.StatusServiceUnavailable, "upstream_unavailable", err.Error())
		return
	}
	s.upHealthMu.Lock()
	s.upHealthOK = true
	s.upHealthAt = time.Now()
	s.upHealthMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"upstream": "up", "cached": false})
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
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
	firstName, ok := aggregator.SanitizeName(r.URL.Query().Get("firstName"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "firstName exceeds 100 characters")
		return
	}
	lastName, ok := aggregator.SanitizeName(r.URL.Query().Get("lastName"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "invalid_param", "lastName exceeds 100 characters")
		return
	}
	payload := ministry.SearchDoctorsPayload{
		StartDate:    now.Format("2006-01-02T15:04:05.000Z"),
		EndDate:      now.AddDate(0, 6, 0).Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: specID,
		ForeasID:     foreasID,
		PrefectureID: parseIntPtr(r.URL.Query().Get("prefectureId")),
		FirstName:    firstName,
		LastName:     lastName,
	}
	docs, err := dc.SearchDoctors(r.Context(), payload)
	if err != nil {
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
		return
	}
	limit, offset := pagination(r)
	page := paginate(docs, limit, offset)
	writeJSON(w, http.StatusOK, map[string]any{
		"data": page,
		"meta": map[string]any{"total": len(docs), "limit": limit, "offset": offset},
	})
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
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
	// Family-doctor mode only has one effective specialty (ΓΕΝΙΚΗ/ΟΙΚΟΓΕΝΕΙΑΚΗ
	// ΙΑΤΡΙΚΗ, id=4). Default to it if the caller omits specialtyId.
	const familyDoctorSpecialtyID = 4
	specID := familyDoctorSpecialtyID
	if v := r.URL.Query().Get("specialtyId"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_param", "specialtyId must be an integer")
			return
		}
		specID = n
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
		s.logger.Warn("family-doctor upstream calls failed",
			"docs_err", dErr, "units_err", uErr, "path", r.URL.Path)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"doctors": docs,
		"units":   aggregator.FilterSearchUnits(units),
	})
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
		return
	}
	writeJSON(w, http.StatusOK, aggregator.FilterSearchUnits(units))
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
		return
	}
	writeJSON(w, http.StatusOK, aggregator.FilterSearchUnits(units))
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
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
		s.logger.Warn("upstream ministry call failed", "path", r.URL.Path, "error", err)
		writeJSONError(w, http.StatusBadGateway, "upstream_failure", "ministry API call failed")
		return
	}
	writeJSON(w, http.StatusOK, doors)
}
