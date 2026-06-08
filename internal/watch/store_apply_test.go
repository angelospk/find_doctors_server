package watch

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_ListActiveExcludesExpiredAndCancelled(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	active, _ := s.Create(ctx, sampleWatch())

	expiredW := sampleWatch()
	expiredW.ExpiresAt = time.Now().Add(-time.Hour)
	expired, _ := s.Create(ctx, expiredW)

	cancelled, _ := s.Create(ctx, sampleWatch())
	if err := s.Delete(ctx, cancelled.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	list, err := s.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(list) != 1 || list[0].ID != active.ID {
		t.Fatalf("expected only %s active, got %+v", active.ID, list)
	}
	_ = expired
}

func TestStore_DeleteIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	w, _ := s.Create(ctx, sampleWatch())

	if err := s.Delete(ctx, w.ID); err != nil {
		t.Fatalf("first Delete: %v", err)
	}
	if err := s.Delete(ctx, w.ID); err != nil {
		t.Fatalf("second Delete should be a no-op, got: %v", err)
	}
	got, _ := s.Get(ctx, w.ID)
	if got.Status != StatusCancelled {
		t.Errorf("expected cancelled, got %q", got.Status)
	}
}

func TestStore_ReloadFromSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s1, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	created, _ := s1.Create(context.Background(), sampleWatch())

	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen NewStore: %v", err)
	}
	got, err := s2.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.ID != created.ID || got.HUnitID != created.HUnitID {
		t.Errorf("reloaded watch mismatch: %+v", got)
	}
}

func TestStore_ApplyMutatesActiveWatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	w, _ := s.Create(ctx, sampleWatch())

	d := "2026-07-01"
	changed, err := s.Apply(ctx, w.ID, func(w *Watch) bool {
		w.CurrentDate = &d
		return true
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !changed {
		t.Error("expected changed=true")
	}
	got, _ := s.Get(ctx, w.ID)
	if got.CurrentDate == nil || *got.CurrentDate != d {
		t.Errorf("expected CurrentDate %q, got %v", d, got.CurrentDate)
	}
}

func TestStore_ApplyIsNoOpOnCancelledWatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	w, _ := s.Create(ctx, sampleWatch())
	_ = s.Delete(ctx, w.ID)

	called := false
	changed, err := s.Apply(ctx, w.ID, func(w *Watch) bool {
		called = true
		return true
	})
	if err != nil {
		t.Fatalf("Apply on cancelled should not error, got: %v", err)
	}
	if called {
		t.Error("fn must not run on a cancelled watch")
	}
	if changed {
		t.Error("expected changed=false for cancelled watch")
	}
}
