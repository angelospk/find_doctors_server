package ministry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncBuf is a goroutine-safe log sink (logins log from their own goroutine).
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func captureLogs(t *testing.T) *syncBuf {
	t.Helper()
	buf := &syncBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// The 2026-10-13 incident: after a burst, every call on our one HTTP/2 connection hung
// (no 403, no answer) while a new connection was answered at once. Re-logging in over
// the same dead connection hung too, so only a restart fixed it. Here the server wedges
// the first connection it sees and answers every other one.
func TestStallOpensNewConnectionAndRelogsIn(t *testing.T) {
	var mu sync.Mutex
	wedged := ""
	addrs := map[string]bool{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if wedged == "" {
			wedged = r.RemoteAddr
		}
		addrs[r.RemoteAddr] = true
		stuck := r.RemoteAddr == wedged
		mu.Unlock()
		if stuck {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	base := srv.Client().Transport.(*http.Transport)
	c := NewClient(srv.URL)
	c.newTransport = func() *http.Transport { return base.Clone() }
	c.resetTransport()
	c.MaxRetries = 1

	var logins atomic.Int32
	c.EnableAutoLogin("u", "p", "a")
	c.auto.login = func(context.Context, string, string, string) error { logins.Add(1); return nil }

	for i := 0; i < stallLimit; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		if err := c.Ping(ctx); err == nil {
			t.Fatalf("call %d on the wedged connection answered", i)
		}
		cancel()
	}

	deadline := time.Now().Add(2 * time.Second)
	for logins.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1 after %d unanswered calls", logins.Load(), stallLimit)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("after the stall the client still uses the dead connection: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(addrs) < 2 {
		t.Fatalf("server saw %d connection(s), want a new one after the stall", len(addrs))
	}
}

// A stall without TaxisNet credentials still gets a new connection (the session may be
// a bridge cookie that is fine).
func TestStallWithoutAutoLoginStillResetsConnection(t *testing.T) {
	c := NewClient("https://example.invalid")
	before := c.currentTransport()
	for i := 0; i < stallLimit; i++ {
		c.noteStall(context.Background(), context.DeadlineExceeded, c.gen.Load())
	}
	deadline := time.Now().Add(time.Second)
	for c.currentTransport() == before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.currentTransport() == before {
		t.Fatal("transport not replaced after a stall")
	}
}

// The login must go through the client's (replaceable) transport, not
// http.DefaultTransport: on the shared default it hung on the same dead connection.
func TestLoginUsesTheClientTransport(t *testing.T) {
	c := NewClient("https://example.invalid")
	jar, _ := cookiejar.New(nil)
	hc := c.loginHTTPClient(jar)
	if hc.Transport == nil || hc.Transport == http.DefaultTransport {
		t.Fatal("login uses http.DefaultTransport")
	}
	if hc.Transport != c.HTTPClient.Transport {
		t.Fatal("login and API calls use different transports")
	}
	if hc.Jar != jar {
		t.Fatal("login client lost its cookie jar")
	}
}

// HTTP/2 health checks: a connection that answers nothing (not even a ping) is closed
// on its own instead of holding every call until its timeout.
func TestTransportPingsIdleHTTP2Connections(t *testing.T) {
	tr := defaultTransport()
	if tr == http.DefaultTransport {
		t.Fatal("must not share http.DefaultTransport")
	}
	if tr.HTTP2 == nil || tr.HTTP2.SendPingTimeout <= 0 || tr.HTTP2.PingTimeout <= 0 {
		t.Fatalf("HTTP/2 ping health check not configured: %+v", tr.HTTP2)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Fatal("HTTP/2 must stay enabled on the custom transport")
	}
}

// Every re-login says what happened; before, a failed one left no trace.
func TestReloginIsLogged(t *testing.T) {
	logs := captureLogs(t)
	c := autoClient(func(context.Context) error { return errors.New("taxisnet down") })
	_ = c.ensureFreshSession(context.Background())
	c2 := autoClient(func(context.Context) error { return nil })
	_ = c2.ensureFreshSession(context.Background())

	out := logs.String()
	if !strings.Contains(out, "taxisnet re-login failed") || !strings.Contains(out, "taxisnet down") {
		t.Fatalf("failed login not logged:\n%s", out)
	}
	if !strings.Contains(out, "taxisnet re-login ok") {
		t.Fatalf("successful login not logged:\n%s", out)
	}
	if strings.Contains(out, "password") || strings.Contains(out, `"p"`) {
		t.Fatalf("credentials in the log:\n%s", out)
	}
}

// A stall says so in the log, with the action taken.
func TestStallIsLogged(t *testing.T) {
	logs := captureLogs(t)
	c := NewClient("https://example.invalid")
	for i := 0; i < stallLimit; i++ {
		c.noteStall(context.Background(), context.DeadlineExceeded, c.gen.Load())
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(logs.String(), "ministry stall") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), "ministry stall") {
		t.Fatalf("stall not logged:\n%s", logs.String())
	}
}

// A burst of calls on the dead connection times out together. That is ONE stall: one new
// connection and one login, and the late failures of calls still on the old connection
// must not replace the new, healthy one.
func TestConcurrentTimeoutsRecoverOnce(t *testing.T) {
	c := NewClient("https://example.invalid")
	var built atomic.Int32
	c.newTransport = func() *http.Transport { built.Add(1); return &http.Transport{} }
	var logins atomic.Int32
	c.EnableAutoLogin("u", "p", "a")
	c.auto.login = func(context.Context, string, string, string) error { logins.Add(1); return nil }

	gen := c.gen.Load() // every call below started on the same connection
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.noteStall(context.Background(), context.DeadlineExceeded, gen) }()
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	if built.Load() != 1 {
		t.Fatalf("transport replaced %d times, want 1", built.Load())
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1", logins.Load())
	}
}

// A login that succeeded a few seconds before the stall (the 30 s cooldown) must not
// stop the stall's own re-login.
func TestStallReloginIgnoresTheCooldown(t *testing.T) {
	c := NewClient("https://example.invalid")
	c.newTransport = func() *http.Transport { return &http.Transport{} }
	var logins atomic.Int32
	c.EnableAutoLogin("u", "p", "a")
	c.auto.login = func(context.Context, string, string, string) error { logins.Add(1); return nil }
	if err := c.ensureFreshSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < stallLimit; i++ {
		c.noteStall(context.Background(), context.DeadlineExceeded, c.gen.Load())
	}
	deadline := time.Now().Add(time.Second)
	for logins.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if logins.Load() != 2 {
		t.Fatalf("logins = %d, want 2 (the stall must log in again despite the cooldown)", logins.Load())
	}
}

// Login errors carry response bodies (checkamka echoes patient data) and OAuth URLs
// with authorization codes: the log keeps the stage only.
func TestReloginLogHidesBodiesAndURLs(t *testing.T) {
	logs := captureLogs(t)
	for _, e := range []error{
		errors.New(`checkamka status 500: {"amka":"12345678901","name":"ΠΑΠΑΔΟΠΟΥΛΟΣ"}`),
		errors.New(`login: Get "https://oauth2.gsis.gr/cb?code=SECRETCODE": EOF`),
	} {
		e := e
		c := autoClient(func(context.Context) error { return e })
		_ = c.ensureFreshSession(context.Background())
	}
	out := logs.String()
	for _, leak := range []string{"12345678901", "ΠΑΠΑΔΟΠΟΥΛΟΣ", "SECRETCODE", "oauth2.gsis.gr"} {
		if strings.Contains(out, leak) {
			t.Fatalf("log leaks %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "checkamka status 500") || !strings.Contains(out, "stage=login") {
		t.Fatalf("log lost the stage:\n%s", out)
	}
}

// A stall's login must not settle for a login that was already running on the retired
// connection and then failed: it waits for it, then logs in itself.
func TestStallLoginAfterAFailedInflightLogin(t *testing.T) {
	c := NewClient("https://example.invalid")
	c.newTransport = func() *http.Transport { return &http.Transport{} }
	release := make(chan struct{})
	var logins atomic.Int32
	c.EnableAutoLogin("u", "p", "a")
	c.auto.login = func(context.Context, string, string, string) error {
		if logins.Add(1) == 1 {
			<-release
			return errors.New("timeout on the dead connection")
		}
		return nil
	}
	go func() { _ = c.ensureFreshSession(context.Background()) }() // the 403 login, on the old connection
	for logins.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	done := make(chan error, 1)
	go func() { done <- c.ensureSession(context.Background(), true) }()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("forced login after a failed one: %v", err)
	}
	if logins.Load() != 2 {
		t.Fatalf("logins = %d, want 2", logins.Load())
	}
}

// A request is tagged with the connection it actually uses: a swap between reading the
// generation and sending must not mislabel it.
func TestRequestUsesTheConnectionOfItsGeneration(t *testing.T) {
	c := NewClient("https://example.invalid")
	c.newTransport = func() *http.Transport { return &http.Transport{} }
	old := c.conn()
	c.resetTransport()
	cur := c.conn()
	if cur == old || cur.gen == old.gen || c.gen.Load() != cur.gen {
		t.Fatalf("swap must start a new generation: old=%d cur=%d client=%d", old.gen, cur.gen, c.gen.Load())
	}
	req, _ := http.NewRequestWithContext(withConn(context.Background(), old), http.MethodGet, "https://example.invalid", nil)
	if got := connOf(req, c); got != old {
		t.Fatal("a request started on the old connection must stay on it")
	}
	req2, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	if got := connOf(req2, c); got != cur {
		t.Fatal("an untagged request must use the current connection")
	}
}
