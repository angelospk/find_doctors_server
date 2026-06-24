package ministry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Browserless TaxisNet login. finddoctors' session was historically obtained via a
// real browser (a Playwright "bridge"), but the whole flow is in fact a plain
// OAuth2 Authorization-Code + a patient-identification POST that an HTTP client can
// drive end-to-end — no browser. Verified live 2026-06-24. Flow:
//   1. GET  gsis authorize            → login form
//   2. POST gsis j_spring_security_check (j_username/j_password, no CSRF)
//   3. POST gsis oauth/authorize (user_oauth_approval=true) → redirect to finddoctors
//      with ?code=…, which finddoctors exchanges server-side, setting the 3 cookies
//   4. GET  /api/v1/auth/taxisnet-session  → the user's ΑΦΜ
//   5. POST /api/v1/patienterv/checkamka {amkaInput, a_afm, isTaxisnet:1} → identified
// A single cookie jar carries everything (a mid-flow JSESSIONID rotation means a
// snapshot Cookie header would 403 on step 5 — the jar handles it). On success the
// 3 finddoctors cookies are pushed to the client's session for direct data calls.

const (
	oauthClientID  = "LJILQZ43117"
	loginUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// Origins for the login flow. Overridable so tests can point them at an httptest
// server; production uses the real gsis.gr SSO + finddoctors.gov.gr.
var (
	gsisBaseURL = "https://oauth2.gsis.gr"
	fdBaseURL   = "https://www.finddoctors.gov.gr"
)

func authorizeURL() string {
	return gsisBaseURL + "/oauth2server/oauth/authorize?response_type=code&client_id=" + oauthClientID +
		"&redirect_uri=" + url.QueryEscape(fdBaseURL+"/p-appointment/login/oauth2/code/taxisnet/") + "&scope=read"
}

// auto-login config + a cooldown so concurrent 403s trigger at most one re-login.
type autoLogin struct {
	mu       sync.Mutex
	username string
	password string
	amka     string
	lastAt   time.Time
}

// EnableAutoLogin stores TaxisNet credentials + ΑΜΚΑ so the client can (re)establish
// its own session with no browser. Safe to call once at startup.
func (c *Client) EnableAutoLogin(username, password, amka string) {
	if c.auto == nil {
		c.auto = &autoLogin{}
	}
	c.auto.mu.Lock()
	c.auto.username, c.auto.password, c.auto.amka = username, password, amka
	c.auto.mu.Unlock()
}

// AutoLoginEnabled reports whether credentials are configured.
func (c *Client) AutoLoginEnabled() bool {
	return c.auto != nil && c.auto.username != "" && c.auto.password != "" && c.auto.amka != ""
}

// ensureFreshSession runs a login if one hasn't run very recently (cooldown), so a
// burst of 403s collapses into a single re-login. Returns the login error (if any).
func (c *Client) ensureFreshSession(ctx context.Context) error {
	if !c.AutoLoginEnabled() {
		return fmt.Errorf("auto-login not configured")
	}
	c.auto.mu.Lock()
	if time.Since(c.auto.lastAt) < 30*time.Second {
		c.auto.mu.Unlock()
		return nil
	}
	c.auto.lastAt = time.Now()
	user, pass, amka := c.auto.username, c.auto.password, c.auto.amka
	c.auto.mu.Unlock()
	return c.LoginTaxisnet(ctx, user, pass, amka)
}

// LoginTaxisnet performs the full browserless login + ΑΜΚΑ identification and, on
// success, stores the finddoctors session cookies on the client.
func (c *Client) LoginTaxisnet(ctx context.Context, username, password, amka string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("cookiejar: %w", err)
	}
	hc := &http.Client{Timeout: 30 * time.Second, Jar: jar}

	do := func(method, urlStr, contentType string, body io.Reader) (*http.Response, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", loginUserAgent)
		req.Header.Set("Accept", "application/json, text/plain, text/html, */*")
		if strings.HasPrefix(urlStr, fdBaseURL) {
			// Match what the SPA sends on its API calls.
			req.Header.Set("Authorization", "no-auth")
			req.Header.Set("Referer", fdBaseURL+"/p-appointment/")
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		res, err := hc.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = res.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return res, data, nil
	}

	fdHost, _ := url.Parse(fdBaseURL)
	// 1. authorize → login form (gsis session cookie set in the jar).
	if _, _, err := do(http.MethodGet, authorizeURL(), "", nil); err != nil {
		return fmt.Errorf("authorize: %w", err)
	}
	// 2. credentials. Go follows the 302 chain back to authorize automatically.
	creds := url.Values{"j_username": {username}, "j_password": {password}}
	res, _, err := do(http.MethodPost, gsisBaseURL+"/oauth2server/j_spring_security_check", "application/x-www-form-urlencoded", strings.NewReader(creds.Encode()))
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	// 3. consent (first login only; SSO-alive auto-redirects straight to finddoctors).
	if res.Request == nil || res.Request.URL.Host != fdHost.Host {
		approve := url.Values{"user_oauth_approval": {"true"}, "scope.read": {"true"}}
		if _, _, err := do(http.MethodPost, gsisBaseURL+"/oauth2server/oauth/authorize", "application/x-www-form-urlencoded", strings.NewReader(approve.Encode())); err != nil {
			return fmt.Errorf("approval: %w", err)
		}
	}
	// Harvest at the /p-appointment/ path: JSESSIONID is scoped to Path=/p-appointment,
	// so jar.Cookies("/") would silently drop it (leaving only the Path=/ cookies) → 403.
	fdURL, _ := url.Parse(fdBaseURL + "/p-appointment/")
	if len(jar.Cookies(fdURL)) == 0 {
		return fmt.Errorf("no finddoctors cookies after oauth (login likely failed)")
	}

	// 4. taxisnet-session → ΑΦΜ.
	_, tsBody, err := do(http.MethodGet, fdBaseURL+"/p-appointment/api/v1/auth/taxisnet-session", "", nil)
	if err != nil {
		return fmt.Errorf("taxisnet-session: %w", err)
	}
	var ts struct {
		AFM string `json:"afm"`
	}
	if err := json.Unmarshal(tsBody, &ts); err != nil || ts.AFM == "" {
		return fmt.Errorf("taxisnet-session: no afm (body: %s)", truncateBody(tsBody))
	}

	// 5. checkamka → establishes patient identification (rv/* endpoints unlock).
	payload, _ := json.Marshal(map[string]any{"amkaInput": amka, "a_afm": ts.AFM, "isTaxisnet": 1})
	ckRes, ckBody, err := do(http.MethodPost, fdBaseURL+"/p-appointment/api/v1/patienterv/checkamka", "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("checkamka: %w", err)
	}
	if ckRes.StatusCode != http.StatusOK {
		return fmt.Errorf("checkamka status %d: %s", ckRes.StatusCode, truncateBody(ckBody))
	}

	// Harvest the 3 finddoctors cookies for the client's direct data calls.
	var parts []string
	for _, ck := range jar.Cookies(fdURL) {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	if len(parts) == 0 {
		return fmt.Errorf("no cookies to harvest after identification")
	}
	c.SetSessionCookie(strings.Join(parts, "; "))
	return nil
}

func truncateBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
