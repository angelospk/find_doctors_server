package ministry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Client handles communication with the Hellenic Ministry API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client

	cache *TTLCache
	sf    *singleflight

	// sem caps total concurrent upstream calls. nil = unlimited.
	sem chan struct{}

	// session holds the current JSESSIONID value obtained from a TaxisNet login.
	// As of finddoctors v2.0.3 every upstream call is gated behind this session
	// cookie; without it the Ministry returns 403. Updated at runtime via the
	// local session endpoint and read on every request, so it is stored atomically.
	session atomic.Pointer[string]

	// Retry knobs. Zero values fall back to sensible defaults in retryDo.
	MaxRetries int
	RetryWait  time.Duration

	// auto holds optional TaxisNet credentials for browserless (re)login. nil = off.
	auto *autoLogin
}

// NewClient creates a new Ministry API client.
// baseURL is typically "https://www.finddoctors.gov.gr"
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		cache:      NewTTLCache(),
		sf:         newSingleflight(),
		MaxRetries: 2,
		RetryWait:  200 * time.Millisecond,
	}
}

// SetMaxConcurrency caps how many upstream requests may be in flight at once.
// A non-positive value disables the limiter. Pass via env at startup.
func (c *Client) SetMaxConcurrency(n int) {
	if n <= 0 {
		c.sem = nil
		return
	}
	c.sem = make(chan struct{}, n)
}

// acquire blocks until a slot is free or the context is cancelled.
func (c *Client) acquire(ctx context.Context) error {
	if c.sem == nil {
		return nil
	}
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) release() {
	if c.sem == nil {
		return
	}
	<-c.sem
}

// Cache exposes the underlying TTL cache (admin flush, tests).
func (c *Client) Cache() *TTLCache { return c.cache }

func (c *Client) setStdHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "no-auth")
	if req.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", "https://www.finddoctors.gov.gr")
	req.Header.Set("Referer", "https://www.finddoctors.gov.gr/p-appointment/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	if cookie := c.SessionCookie(); cookie != "" {
		// Accept either a full Cookie header ("JSESSIONID=…; FindDoc=…; cookiesession1=…")
		// or a bare JSESSIONID value. The Ministry needs ALL THREE finddoctors cookies
		// (JSESSIONID + httpOnly FindDoc + cookiesession1) — a bare JSESSIONID gets 403.
		if strings.Contains(cookie, "=") {
			req.Header.Set("Cookie", cookie)
		} else {
			req.Header.Set("Cookie", "JSESSIONID="+cookie)
		}
	}
}

// SetSessionCookie updates the cookies used on all subsequent upstream calls.
// Pass a full Cookie header value (preferred) or a bare JSESSIONID. Concurrent-safe.
func (c *Client) SetSessionCookie(cookie string) {
	v := cookie
	c.session.Store(&v)
}

// SessionCookie returns the current cookie header (or bare JSESSIONID), or "".
func (c *Client) SessionCookie() string {
	if p := c.session.Load(); p != nil {
		return *p
	}
	return ""
}

// HasSession reports whether a session cookie is currently configured.
func (c *Client) HasSession() bool { return c.SessionCookie() != "" }

// retryableStatus reports whether an HTTP status code is worth retrying.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusInternalServerError:
		return true
	}
	return false
}

// endpointLabel collapses a full URL to a short label suitable for metrics
// cardinality (e.g. "/p-appointment/api/v1/rv/searchhunits" → "rv/searchhunits").
func endpointLabel(url string) string {
	const marker = "/api/v1/"
	i := stringIndex(url, marker)
	if i < 0 {
		return "unknown"
	}
	return url[i+len(marker):]
}

func stringIndex(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// doJSON sends a JSON request (GET or POST), retrying transient failures, and
// decodes the response body into out. The body is rebuilt per attempt so retries
// are safe.
func (c *Client) doJSON(ctx context.Context, method, url string, body any, out any) error {
	var encoded []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		encoded = b
	}

	max := c.MaxRetries
	if max <= 0 {
		max = 1
	}
	wait := c.RetryWait
	if wait <= 0 {
		wait = 200 * time.Millisecond
	}

	endpoint := endpointLabel(url)
	var lastErr error
	for attempt := 0; attempt < max; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		start := time.Now()

		var rdr io.Reader
		if encoded != nil {
			rdr = bytes.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, rdr)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		c.setStdHeaders(req)

		if err := c.acquire(ctx); err != nil {
			return err
		}
		res, err := c.HTTPClient.Do(req)
		c.release()
		apiLatency.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())
		if err != nil {
			apiCallsTotal.WithLabelValues(endpoint, "network_error").Inc()
			// Never retry caller cancellations or deadline overruns.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			lastErr = err
		} else {
			if res.StatusCode == http.StatusOK {
				defer func() { _ = res.Body.Close() }()
				apiCallsTotal.WithLabelValues(endpoint, "success").Inc()
				if out == nil {
					_, _ = io.Copy(io.Discard, res.Body)
					return nil
				}
				if err := json.NewDecoder(res.Body).Decode(out); err != nil {
					return fmt.Errorf("decode response: %w", err)
				}
				return nil
			}
			// Drain & close so the connection can be reused.
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			apiCallsTotal.WithLabelValues(endpoint, fmt.Sprintf("status_%d", res.StatusCode)).Inc()
			// 403 = session expired/missing. With auto-login, re-establish it (browserless)
			// and retry; the next attempt re-reads the refreshed session cookie.
			if res.StatusCode == http.StatusForbidden && c.AutoLoginEnabled() {
				if lerr := c.ensureFreshSession(ctx); lerr != nil {
					return fmt.Errorf("auto re-login after 403 failed: %w", lerr)
				}
				lastErr = fmt.Errorf("upstream returned 403; re-logged in")
				continue
			}
			if !retryableStatus(res.StatusCode) {
				return fmt.Errorf("unexpected status code: %d", res.StatusCode)
			}
			lastErr = fmt.Errorf("upstream returned %d", res.StatusCode)
		}

		// Backoff before next attempt (unless this was the last).
		if attempt < max-1 {
			apiRetries.WithLabelValues(endpoint).Inc()
			// Jittered backoff: base ± 25% so concurrent clients don't
			// retry in lockstep against a recovering upstream.
			base := wait * time.Duration(1<<attempt)
			jitter := time.Duration(rand.Int63n(int64(base)/2 + 1))
			delay := base - base/4 + jitter
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("ministry: exhausted retries")
	}
	return lastErr
}

// SearchHUnits queries the /rv/searchhunits endpoint to find hospitals or PFYs.
func (c *Client) SearchHUnits(ctx context.Context, payload SearchPayload) ([]HUnit, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/rv/searchhunits", c.BaseURL)

	var units []HUnit
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &units); err != nil {
		return nil, err
	}

	// The Ministry API doesn't return the ForeasID in the response items,
	// so we inject it here to preserve the context of where this unit came from.
	for i := range units {
		units[i].ForeasID = payload.ForeasID
	}
	return units, nil
}

// FirstAvailableSlot queries the /rv/firstavailableslot endpoint to find the earliest appointment date.
func (c *Client) FirstAvailableSlot(ctx context.Context, payload SearchPayload) (string, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/rv/firstavailableslot", c.BaseURL)
	var dateStr string
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &dateStr); err != nil {
		return "", err
	}
	return dateStr, nil
}

// GetSpecialties retrieves all medical specialties metadata. Cached for 24h.
func (c *Client) GetSpecialties(ctx context.Context) ([]Specialty, error) {
	const key = "specialties"
	if v, ok := c.cache.Get(key); ok {
		observeCacheHit(key)
		return v.([]Specialty), nil
	}
	observeCacheMiss(key)
	v, err := c.sf.Do(key, func() (any, error) {
		// Detach from the caller's ctx so one cancellation does not poison
		// every other goroutine waiting on the same singleflight key.
		fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		url := fmt.Sprintf("%s/p-appointment/api/v1/gen/getspecialities", c.BaseURL)
		var specs []Specialty
		if err := c.doJSON(fetchCtx, http.MethodGet, url, nil, &specs); err != nil {
			return nil, err
		}
		c.cache.Set(key, specs, 24*time.Hour)
		return specs, nil
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return v.([]Specialty), nil
}

// Ping performs a cheap, cache-bypassing GET against a lightweight upstream
// endpoint to confirm the ministry API is actually reachable right now.
// Used by readiness probes so a stale 24h cache can't mask an outage.
func (c *Client) Ping(ctx context.Context) error {
	url := fmt.Sprintf("%s/p-appointment/api/v1/gen/getspecialities", c.BaseURL)
	// out == nil drains the body without decoding, keeping this cheap.
	return c.doJSON(ctx, http.MethodGet, url, nil, nil)
}

// GetHealthUnitTypes retrieves the foreas/health-unit-types catalog.
// Trailing slash on the path is required by the upstream API.
// Cached for 24h.
func (c *Client) GetHealthUnitTypes(ctx context.Context) ([]HealthUnitType, error) {
	const key = "healthUnitTypes"
	if v, ok := c.cache.Get(key); ok {
		observeCacheHit(key)
		return v.([]HealthUnitType), nil
	}
	observeCacheMiss(key)
	v, err := c.sf.Do(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		url := fmt.Sprintf("%s/p-appointment/api/v1/gen/getHealthUnitTypes/", c.BaseURL)
		var types []HealthUnitType
		if err := c.doJSON(fetchCtx, http.MethodGet, url, nil, &types); err != nil {
			return nil, err
		}
		c.cache.Set(key, types, 24*time.Hour)
		return types, nil
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return v.([]HealthUnitType), nil
}

// GetPrefectures retrieves the standard 51-prefecture catalog. Cached for 24h.
func (c *Client) GetPrefectures(ctx context.Context) ([]Prefecture, error) {
	return c.fetchPrefectures(ctx, "prefectures", "/p-appointment/api/v1/gen/getprefectures")
}

// GetCovidPrefectures retrieves the prefectures that host COVID vaccination centers.
func (c *Client) GetCovidPrefectures(ctx context.Context) ([]Prefecture, error) {
	return c.fetchPrefectures(ctx, "prefectures.covid", "/p-appointment/api/v1/gen/getprefecturescovid")
}

// GetMentalHealthPrefectures retrieves the prefectures with mental health units.
func (c *Client) GetMentalHealthPrefectures(ctx context.Context) ([]Prefecture, error) {
	return c.fetchPrefectures(ctx, "prefectures.mh", "/p-appointment/api/v1/gen/getprefecturesmentalhealth")
}

func (c *Client) fetchPrefectures(ctx context.Context, cacheKey, path string) ([]Prefecture, error) {
	if v, ok := c.cache.Get(cacheKey); ok {
		observeCacheHit(cacheKey)
		return v.([]Prefecture), nil
	}
	observeCacheMiss(cacheKey)
	v, err := c.sf.Do(cacheKey, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		url := c.BaseURL + path
		var prefs []Prefecture
		if err := c.doJSON(fetchCtx, http.MethodGet, url, nil, &prefs); err != nil {
			return nil, err
		}
		c.cache.Set(cacheKey, prefs, 24*time.Hour)
		return prefs, nil
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return v.([]Prefecture), nil
}

// GetSlotsInit retrieves the capacity calendar for a specific unit and specialty.
func (c *Client) GetSlotsInit(ctx context.Context, payload SlotsInitPayload) ([]SlotGroup, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/rv/getslotsinit", c.BaseURL)
	var groups []SlotGroup
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

// GetActualSlots retrieves specific appointment times for a chosen day and group.
func (c *Client) GetActualSlots(ctx context.Context, payload GetActualSlotsPayload) ([]ActualSlot, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/rv/getactualslots", c.BaseURL)
	var slots []ActualSlot
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &slots); err != nil {
		return nil, err
	}
	return slots, nil
}

// SearchDoctors queries /rv/searchdoctors to find named physicians.
// Used for EOPYY-affiliated private doctors (foreasID=19) and private doctors (foreasID=20).
func (c *Client) SearchDoctors(ctx context.Context, payload SearchDoctorsPayload) ([]Doctor, error) {
	return c.searchDoctorsPath(ctx, "/p-appointment/api/v1/rv/searchdoctors", payload)
}

// SearchDoctorsByLocation queries /rv/searchdoctors/currentlocation for geo-filtered results.
func (c *Client) SearchDoctorsByLocation(ctx context.Context, payload SearchDoctorsPayload) ([]Doctor, error) {
	return c.searchDoctorsPath(ctx, "/p-appointment/api/v1/rv/searchdoctors/currentlocation", payload)
}

// SearchDoctorsFD queries /rv/searchdoctorsfd for Family/Personal Doctor results.
func (c *Client) SearchDoctorsFD(ctx context.Context, payload SearchDoctorsPayload) ([]Doctor, error) {
	payload.IsOnlyFd = 1
	return c.searchDoctorsPath(ctx, "/p-appointment/api/v1/rv/searchdoctorsfd", payload)
}

func (c *Client) searchDoctorsPath(ctx context.Context, path string, payload SearchDoctorsPayload) ([]Doctor, error) {
	url := c.BaseURL + path
	var doctors []Doctor
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &doctors); err != nil {
		return nil, err
	}
	// Filter placeholder responseCode:2 entries (empty/null-filled objects).
	out := doctors[:0]
	for _, d := range doctors {
		if d.ResponseCode == 2 {
			continue
		}
		if d.FirstName == "" && d.LastName == "" && d.Amka == "" {
			continue
		}
		d.ForeasID = payload.ForeasID
		out = append(out, d)
	}
	return out, nil
}

// SearchHunitsFD queries /rv/searchhunitsfd for FD-mode hospitals.
func (c *Client) SearchHunitsFD(ctx context.Context, payload SearchPayload) ([]HUnit, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/rv/searchhunitsfd", c.BaseURL)
	payload.IsOnlyFd = 1
	var units []HUnit
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &units); err != nil {
		return nil, err
	}
	for i := range units {
		units[i].ForeasID = payload.ForeasID
	}
	return units, nil
}

// GetClinicDoors fetches the clinic-door breakdown for a given (hunit, specialty).
func (c *Client) GetClinicDoors(ctx context.Context, hunitID, specialtyID int) ([]ClinicDoor, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/gen/cdoorsbyhunitspeciality?hunitId=%d&specialityID=%d", c.BaseURL, hunitID, specialtyID)
	var doors []ClinicDoor
	if err := c.doJSON(ctx, http.MethodGet, url, nil, &doors); err != nil {
		return nil, err
	}
	return doors, nil
}

// GetMachineRvTypes fetches the diagnostic-machine RV-type catalog. Cached 24h.
func (c *Client) GetMachineRvTypes(ctx context.Context) ([]MachineRvType, error) {
	const key = "machineRvTypes"
	if v, ok := c.cache.Get(key); ok {
		observeCacheHit(key)
		return v.([]MachineRvType), nil
	}
	observeCacheMiss(key)
	v, err := c.sf.Do(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		url := fmt.Sprintf("%s/p-appointment/api/v1/machines/getMachineRvTypes", c.BaseURL)
		var types []MachineRvType
		if err := c.doJSON(fetchCtx, http.MethodGet, url, nil, &types); err != nil {
			return nil, err
		}
		c.cache.Set(key, types, 24*time.Hour)
		return types, nil
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return v.([]MachineRvType), nil
}

// SearchHunitsMachines queries /machines/searchHunitsMachines for diagnostic-machine units.
func (c *Client) SearchHunitsMachines(ctx context.Context, payload SearchPayload) ([]HUnit, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/machines/searchHunitsMachines", c.BaseURL)
	payload.IsMachine = 1
	var units []HUnit
	if err := c.doJSON(ctx, http.MethodPost, url, payload, &units); err != nil {
		return nil, err
	}
	for i := range units {
		units[i].ForeasID = payload.ForeasID
	}
	return units, nil
}
