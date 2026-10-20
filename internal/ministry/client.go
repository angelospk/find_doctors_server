package ministry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

	// stalls counts consecutive calls the upstream never answered (see noteStall), on
	// the connection generation gen. A recovery starts a new generation; failures of
	// calls that started on an older one no longer count.
	stalls atomic.Int32
	gen    atomic.Uint64
	recMu  sync.Mutex

	// tr is the connection pool every call (and the TaxisNet login) goes through, with
	// its generation; a stall replaces it. newTransport builds one (nil = defaultTransport;
	// tests inject theirs).
	tr           atomic.Pointer[connGen]
	newTransport func() *http.Transport
}

// connGen is one connection pool and the generation it belongs to. A request carries
// the one it was sent on, so its failure is counted against the right generation.
type connGen struct {
	t   *http.Transport
	gen uint64
}

// defaultTransport is our own pool, never http.DefaultTransport: finddoctors.gov.gr
// speaks HTTP/2, so every call shares one connection, and a connection that stops
// answering must be found and dropped. The ping closes one that answers nothing at all.
func defaultTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ForceAttemptHTTP2 = true
	t.HTTP2 = &http.HTTP2Config{SendPingTimeout: 15 * time.Second, PingTimeout: 5 * time.Second}
	return t
}

func (c *Client) mkTransport() *http.Transport {
	if c.newTransport != nil {
		return c.newTransport()
	}
	return defaultTransport()
}

// conn is the current pool and its generation.
func (c *Client) conn() *connGen {
	if cg := c.tr.Load(); cg != nil {
		return cg
	}
	c.tr.CompareAndSwap(nil, &connGen{t: c.mkTransport(), gen: c.gen.Load()})
	return c.tr.Load()
}

func (c *Client) currentTransport() *http.Transport { return c.conn().t }

// resetTransport moves every new call to a fresh pool, in a new generation. Calls still
// on the old one end at their own timeout; its connections are closed once they have.
func (c *Client) resetTransport() {
	next := &connGen{t: c.mkTransport(), gen: c.gen.Add(1)}
	if old := c.tr.Swap(next); old != nil {
		old.t.CloseIdleConnections()
		time.AfterFunc(45*time.Second, old.t.CloseIdleConnections)
	}
}

type connKey struct{}

func withConn(ctx context.Context, cg *connGen) context.Context {
	return context.WithValue(ctx, connKey{}, cg)
}

// connOf is the pool a request was started on, else the current one.
func connOf(r *http.Request, c *Client) *connGen {
	if cg, ok := r.Context().Value(connKey{}).(*connGen); ok {
		return cg
	}
	return c.conn()
}

// swapRT sends each request through the client's current transport.
type swapRT struct{ c *Client }

func (s swapRT) RoundTrip(r *http.Request) (*http.Response, error) {
	return connOf(r, s.c).t.RoundTrip(r)
}

// stallLimit: this many calls in a row that got no answer at all means the upstream has
// stopped serving us (a fresh connection is answered at once), so open a new connection
// and log in again.
const stallLimit = 3

// noteStall records one unanswered call that started on connection generation gen, and
// recovers when they add up: once per generation, however many calls time out together.
// A caller that cancelled is not a stall; any answer resets the count.
func (c *Client) noteStall(ctx context.Context, err error, gen uint64) {
	if ctx.Err() == context.Canceled || !isTimeout(err) {
		return
	}
	c.recMu.Lock()
	if gen != c.gen.Load() || c.stalls.Add(1) < stallLimit {
		c.recMu.Unlock()
		return
	}
	c.stalls.Store(0)
	c.resetTransport()
	c.recMu.Unlock()
	go c.reloginAfterStall() //nolint:gosec // G118: detached on purpose; a login cancelled with its request left the session dead (relogin_test.go)
}

// reloginAfterStall runs on the new connection: a re-login over the dead one hangs too
// (the 2026-10-13 stall lasted until a restart). It ignores the post-login cooldown.
func (c *Client) reloginAfterStall() {
	if !c.AutoLoginEnabled() {
		slog.Default().Warn("ministry stall: no answer to several calls in a row; new connection (auto-login off)")
		return
	}
	slog.Default().Warn("ministry stall: no answer to several calls in a row; new connection and re-login")
	_ = c.ensureSession(context.Background(), true)
}

// loginHTTPClient is the TaxisNet login's client: its own cookie jar, our transport.
func (c *Client) loginHTTPClient(jar http.CookieJar) *http.Client {
	var rt http.RoundTripper = swapRT{c}
	if c.HTTPClient != nil && c.HTTPClient.Transport != nil {
		rt = c.HTTPClient.Transport
	}
	return &http.Client{Timeout: 30 * time.Second, Jar: jar, Transport: rt}
}

func isTimeout(err error) bool {
	var ne interface{ Timeout() bool }
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

// NewClient creates a new Ministry API client.
// baseURL is typically "https://www.finddoctors.gov.gr"
func NewClient(baseURL string) *Client {
	c := &Client{
		BaseURL:    baseURL,
		cache:      NewTTLCache(),
		sf:         newSingleflight(),
		MaxRetries: 2,
		RetryWait:  200 * time.Millisecond,
	}
	c.HTTPClient = &http.Client{Timeout: 30 * time.Second, Transport: swapRT{c}}
	return c
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
// 429 (Too Many Requests) is included so upstream rate-limiting backs off
// instead of aborting the whole scan on the first throttle.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusInternalServerError:
		return true
	}
	return false
}

// maxRetryAfter caps how long we honor an upstream Retry-After hint. A
// misbehaving server advertising minutes must not stall a fan-out scan.
const maxRetryAfter = 30 * time.Second

// parseRetryAfter interprets a Retry-After header value, which may be either
// delta-seconds (an integer) or an HTTP-date. It returns the delay to wait and
// whether the value was understood. Negative/past or unparseable values return
// ok=false so the caller falls back to the jittered backoff.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
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
		// retryAfter holds an upstream-supplied delay (from a 429 Retry-After
		// header) to honor instead of the jittered backoff this attempt.
		var retryAfter time.Duration
		var useRetryAfter bool
		start := time.Now()

		var rdr io.Reader
		if encoded != nil {
			rdr = bytes.NewReader(encoded)
		}
		cg := c.conn()
		req, err := http.NewRequestWithContext(withConn(ctx, cg), method, url, rdr)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		c.setStdHeaders(req)

		if err := c.acquire(ctx); err != nil {
			return err
		}
		gen := cg.gen
		res, err := c.HTTPClient.Do(req)
		c.release()
		apiLatency.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())
		if err != nil {
			c.noteStall(ctx, err, gen)
		} else {
			c.stalls.Store(0)
		}
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
			if res.StatusCode == http.StatusTooManyRequests {
				if d, ok := parseRetryAfter(res.Header.Get("Retry-After"), time.Now()); ok {
					retryAfter, useRetryAfter = d, true
				}
			}
			// Drain & close so the connection can be reused.
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			apiCallsTotal.WithLabelValues(endpoint, fmt.Sprintf("status_%d", res.StatusCode)).Inc()
			// 403 = session expired/missing. With auto-login, re-establish it (browserless)
			// and retry; the next attempt re-reads the refreshed session cookie.
			if res.StatusCode == http.StatusForbidden && c.AutoLoginEnabled() {
				if lerr := c.ensureFreshSession(ctx); lerr != nil {
					return fmt.Errorf("auto re-login after 403 failed: %s", LoginErrorStage(lerr))
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
			var delay time.Duration
			if useRetryAfter {
				// Honor the upstream's own pacing hint, capped so a bad
				// value can't stall the scan.
				delay = retryAfter
				if delay > maxRetryAfter {
					delay = maxRetryAfter
				}
			} else {
				// Jittered backoff: base ± 25% so concurrent clients don't
				// retry in lockstep against a recovering upstream.
				base := wait * time.Duration(1<<attempt)
				jitter := time.Duration(rand.Int63n(int64(base)/2 + 1))
				delay = base - base/4 + jitter
			}
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
	url := fmt.Sprintf("%s/p-appointment/api/v1/gen/cdoorsbyhunitspeciality?hunitId=%d&specialtyId=%d&iscovid19=false", c.BaseURL, hunitID, specialtyID)
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
