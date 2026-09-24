package ministry

import (
	"context"
	"errors"
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
