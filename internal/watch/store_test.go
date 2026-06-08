package watch

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func sampleWatch() Watch {
	pref := 5
	return Watch{
		HUnitID:      718,
		SpecialtyID:  24,
		ForeasID:     1,
		PrefectureID: &pref,
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}
}

func TestStore_CreateAssignsIDAndActiveStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, sampleWatch())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Error("expected a generated ID")
	}
	if created.Status != StatusActive {
		t.Errorf("expected status %q, got %q", StatusActive, created.Status)
	}
	if created.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID || got.HUnitID != 718 || got.SpecialtyID != 24 {
		t.Errorf("Get returned unexpected watch: %+v", got)
	}
}

func TestStore_GetUnknownReturnsErrNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get(context.Background(), "nope"); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
