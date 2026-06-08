package ministry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTLCache_GetSetExpiry(t *testing.T) {
	c := NewTTLCache()
	c.Set("k", 42, 50*time.Millisecond)

	if v, ok := c.Get("k"); !ok || v.(int) != 42 {
		t.Fatalf("expected hit with 42, got %v ok=%v", v, ok)
	}
	time.Sleep(70 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatalf("expected expiry, but cache still hits")
	}
}

func TestClient_GetSpecialties_CachesResult(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"speciality":1,"name":"X"}]`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if _, err := c.GetSpecialties(context.Background()); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if _, err := c.GetSpecialties(context.Background()); err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", got)
	}
}

func TestClient_RetriesTransient5xx(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n < 3 {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.RetryWait = 5 * time.Millisecond
	c.MaxRetries = 3 // override default (2) so this test can verify two retries
	if _, err := c.GetSpecialties(context.Background()); err != nil {
		t.Fatalf("expected eventual success, got error: %v", err)
	}
	if hits != 3 {
		t.Fatalf("expected 3 upstream attempts, got %d", hits)
	}
}

func TestClient_DoesNotRetry4xx(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.RetryWait = 1 * time.Millisecond
	if _, err := c.GetSpecialties(context.Background()); err == nil {
		t.Fatalf("expected error on 400")
	}
	if hits != 1 {
		t.Fatalf("expected exactly 1 attempt on 400, got %d", hits)
	}
}

func TestSingleflight_CollapsesConcurrentCalls(t *testing.T) {
	sf := newSingleflight()
	var calls int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = sf.Do("k", func() (any, error) {
				atomic.AddInt32(&calls, 1)
				time.Sleep(20 * time.Millisecond)
				return 1, nil
			})
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("expected 1 underlying call, got %d", calls)
	}
}
