package ministry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func autoClient(login func(ctx context.Context) error) *Client {
	c := &Client{}
	c.EnableAutoLogin("u", "p", "a")
	c.auto.login = func(ctx context.Context, _, _, _ string) error { return login(ctx) }
	return c
}

// The 21:41 incident: the page request that hit the 403 was cancelled, the re-login
// ran on its context and died with it, and the session stayed dead.
func TestReloginSurvivesCallerCancel(t *testing.T) {
	done := make(chan error, 1)
	c := autoClient(func(ctx context.Context) error {
		select {
		case <-time.After(50 * time.Millisecond):
			done <- ctx.Err()
			return ctx.Err()
		case <-ctx.Done():
			done <- ctx.Err()
			return ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(5 * time.Millisecond); cancel() }()
	_ = c.ensureFreshSession(ctx)
	if err := <-done; err != nil {
		t.Fatalf("login was cancelled with the caller: %v", err)
	}
}

// A failed or cancelled login must not start the 30s "fresh" cooldown.
func TestFailedLoginDoesNotStartCooldown(t *testing.T) {
	var calls atomic.Int32
	fail := true
	c := autoClient(func(context.Context) error {
		calls.Add(1)
		if fail {
			return errors.New("taxisnet down")
		}
		return nil
	})
	if err := c.ensureFreshSession(context.Background()); err == nil {
		t.Fatal("want the login error")
	}
	fail = false
	if err := c.ensureFreshSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("login calls = %d, want 2 (a failure must be retried at once)", calls.Load())
	}
	// A success DOES start the cooldown.
	if err := c.ensureFreshSession(context.Background()); err != nil || calls.Load() != 2 {
		t.Fatalf("calls=%d err=%v, want cooldown after success", calls.Load(), err)
	}
}

// A burst of 403s shares ONE login, and every waiter gets its result.
func TestConcurrentReloginsShareOneLogin(t *testing.T) {
	var calls atomic.Int32
	c := autoClient(func(context.Context) error {
		calls.Add(1)
		time.Sleep(30 * time.Millisecond)
		return nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.ensureFreshSession(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("login calls = %d, want 1", calls.Load())
	}
}

// The 17:02 incident: the upstream stopped answering our session (fresh sessions got
// answers in 70 ms) and every call hung until its deadline. No 403 ever came, so no
// re-login; only a restart helped. A run of timeouts now re-logs in by itself.
func TestTimeoutsTriggerRelogin(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-hang }))
	defer srv.Close()
	defer close(hang)
	var logins atomic.Int32
	c := autoClient(func(context.Context) error { logins.Add(1); return nil })
	c.BaseURL = srv.URL
	c.HTTPClient = &http.Client{Timeout: 20 * time.Millisecond}
	c.MaxRetries = 1
	for i := 0; i < stallLimit-1; i++ {
		_ = c.doJSON(context.Background(), http.MethodGet, srv.URL, nil, nil)
	}
	if logins.Load() != 0 {
		t.Fatalf("re-logged in after %d timeouts, want only at %d", stallLimit-1, stallLimit)
	}
	_ = c.doJSON(context.Background(), http.MethodGet, srv.URL, nil, nil)
	deadline := time.Now().Add(time.Second)
	for logins.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d after %d timeouts, want 1", logins.Load(), stallLimit)
	}
}

// A caller that gives up is not a stalled upstream.
func TestCallerCancelIsNotAStall(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-hang }))
	defer srv.Close()
	defer close(hang)
	var logins atomic.Int32
	c := autoClient(func(context.Context) error { logins.Add(1); return nil })
	c.HTTPClient = &http.Client{}
	c.MaxRetries = 1
	for i := 0; i < stallLimit+2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(5 * time.Millisecond); cancel() }()
		_ = c.doJSON(ctx, http.MethodGet, srv.URL, nil, nil)
	}
	time.Sleep(20 * time.Millisecond)
	if logins.Load() != 0 {
		t.Fatalf("caller cancels triggered %d logins", logins.Load())
	}
}

// A waiter must get the result of the login it joined, not of whatever login finished
// last: failed flight A, then an instant successful B, must not turn A's waiters green.
func TestWaiterGetsItsOwnFlightResult(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		gate := make(chan struct{})
		var failNext atomic.Bool
		failNext.Store(true)
		c := autoClient(func(context.Context) error {
			if failNext.Swap(false) {
				<-gate
				return errors.New("taxisnet down")
			}
			return nil
		})
		const waiters = 8
		joined := make(chan struct{}, 64)
		c.auto.joined = func() { joined <- struct{}{} }
		errs := make(chan error, waiters)
		for i := 0; i < waiters; i++ {
			go func() { errs <- c.ensureFreshSession(context.Background()) }()
		}
		for i := 0; i < waiters; i++ { // every waiter holds flight A before it can finish
			<-joined
		}
		close(gate)
		for b := 0; b < 20; b++ { // successful flights B, C, ... racing A's waiters
			_ = c.ensureFreshSession(context.Background())
			c.auto.mu.Lock()
			c.auto.lastOK = time.Time{} // no cooldown, so the next call logs in again
			c.auto.mu.Unlock()
		}
		for i := 0; i < waiters; i++ {
			if err := <-errs; err == nil {
				t.Fatalf("iter %d: a waiter of the failed login got nil", iter)
			}
		}
	}
}
