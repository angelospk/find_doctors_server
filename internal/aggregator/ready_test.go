package aggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// pingMock implements MinistryClient plus the optional Ping capability so the
// readiness check exercises the cache-bypassing path.
type pingMock struct {
	MockMinistryClient
	pingErr    error
	pingCalled bool
	specCalled bool
}

func (m *pingMock) Ping(ctx context.Context) error {
	m.pingCalled = true
	return m.pingErr
}

func (m *pingMock) GetSpecialties(ctx context.Context) ([]ministry.Specialty, error) {
	m.specCalled = true
	return nil, nil
}

func TestReady_UsesPingAndBypassesCache(t *testing.T) {
	m := &pingMock{}
	agg := New(m)
	if err := agg.Ready(context.Background()); err != nil {
		t.Fatalf("expected ready, got %v", err)
	}
	if !m.pingCalled {
		t.Error("expected Ping to be used for readiness")
	}
	if m.specCalled {
		t.Error("readiness must not rely on cached GetSpecialties")
	}
}

func TestReady_PropagatesPingFailure(t *testing.T) {
	m := &pingMock{pingErr: errors.New("upstream down")}
	agg := New(m)
	if err := agg.Ready(context.Background()); err == nil {
		t.Fatal("expected readiness failure when upstream ping fails")
	}
}

func TestReady_FallsBackWhenNoPing(t *testing.T) {
	called := false
	m := &MockMinistryClient{
		GetSpecialtiesFunc: func(ctx context.Context) ([]ministry.Specialty, error) {
			called = true
			return nil, nil
		},
	}
	agg := New(m)
	if err := agg.Ready(context.Background()); err != nil {
		t.Fatalf("expected ready, got %v", err)
	}
	if !called {
		t.Error("expected GetSpecialties fallback when client lacks Ping")
	}
}
