package watch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// --- mocks ---

type mockChecker struct {
	fn func(call int) (string, error)
	n  int
}

func (m *mockChecker) FirstAvailableSlot(ctx context.Context, _ ministry.SearchPayload) (string, error) {
	m.n++
	return m.fn(m.n)
}

type recordNotifier struct {
	calls    []string // dates it was asked to deliver
	failNext bool
}

func (r *recordNotifier) Notify(ctx context.Context, w Watch, newDate string) (bool, error) {
	r.calls = append(r.calls, newDate)
	if r.failNext {
		r.failNext = false
		return true, errors.New("delivery failed")
	}
	return true, nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newPollerWith(t *testing.T, checker SlotChecker, notifiers ...Notifier) (*Poller, *Store) {
	t.Helper()
	s := newTestStore(t)
	return NewPoller(s, checker, notifiers, quietLogger()), s
}

// seedWatch creates a watch that already has a known baseline date.
func seededWatch(t *testing.T, s *Store, baseline string) Watch {
	t.Helper()
	w, _ := s.Create(context.Background(), sampleWatch())
	_, _ = s.Apply(context.Background(), w.ID, func(w *Watch) bool {
		w.CurrentDate = &baseline
		w.LastNotifiedDate = &baseline
		return true
	})
	got, _ := s.Get(context.Background(), w.ID)
	return got
}

// --- tests ---

func TestPoller_SeedsWithoutNotifying(t *testing.T) {
	ch := &mockChecker{fn: func(int) (string, error) { return "2026-09-01", nil }}
	notif := &recordNotifier{}
	p, s := newPollerWith(t, ch, notif)
	w, _ := s.Create(context.Background(), sampleWatch()) // no dates yet

	if err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, _ := s.Get(context.Background(), w.ID)
	if got.CurrentDate == nil || *got.CurrentDate != "2026-09-01" {
		t.Errorf("expected seeded CurrentDate, got %v", got.CurrentDate)
	}
	if got.LastNotifiedDate == nil || *got.LastNotifiedDate != "2026-09-01" {
		t.Errorf("expected seeded LastNotifiedDate, got %v", got.LastNotifiedDate)
	}
	if len(notif.calls) != 0 {
		t.Errorf("seeding must not notify, got calls %v", notif.calls)
	}
}

func TestPoller_NotifiesOnceOnEarlierDate(t *testing.T) {
	ch := &mockChecker{fn: func(int) (string, error) { return "2026-07-01", nil }}
	notif := &recordNotifier{}
	p, s := newPollerWith(t, ch, notif)
	w := seededWatch(t, s, "2026-09-01")

	_ = p.RunOnce(context.Background()) // earlier -> notify
	_ = p.RunOnce(context.Background()) // same -> no re-notify

	if len(notif.calls) != 1 || notif.calls[0] != "2026-07-01" {
		t.Fatalf("expected exactly one notify for 2026-07-01, got %v", notif.calls)
	}
	got, _ := s.Get(context.Background(), w.ID)
	if got.LastNotifiedDate == nil || *got.LastNotifiedDate != "2026-07-01" {
		t.Errorf("expected LastNotifiedDate advanced, got %v", got.LastNotifiedDate)
	}
}

func TestPoller_LaterDateDoesNotNotify(t *testing.T) {
	ch := &mockChecker{fn: func(int) (string, error) { return "2026-12-01", nil }}
	notif := &recordNotifier{}
	p, s := newPollerWith(t, ch, notif)
	w := seededWatch(t, s, "2026-09-01")

	_ = p.RunOnce(context.Background())

	if len(notif.calls) != 0 {
		t.Errorf("later date must not notify, got %v", notif.calls)
	}
	got, _ := s.Get(context.Background(), w.ID)
	if got.CurrentDate == nil || *got.CurrentDate != "2026-12-01" {
		t.Errorf("expected CurrentDate updated to later date, got %v", got.CurrentDate)
	}
	if *got.LastNotifiedDate != "2026-09-01" {
		t.Errorf("LastNotifiedDate must not change, got %v", *got.LastNotifiedDate)
	}
}

func TestPoller_UpstreamErrorLeavesStateIntact(t *testing.T) {
	ch := &mockChecker{fn: func(int) (string, error) { return "", errors.New("upstream down") }}
	notif := &recordNotifier{}
	p, s := newPollerWith(t, ch, notif)
	w := seededWatch(t, s, "2026-09-01")

	if err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce should not surface per-watch errors: %v", err)
	}
	got, _ := s.Get(context.Background(), w.ID)
	if *got.CurrentDate != "2026-09-01" || *got.LastNotifiedDate != "2026-09-01" {
		t.Errorf("state must be untouched on upstream error, got %+v", got)
	}
	if len(notif.calls) != 0 {
		t.Errorf("no notify on error, got %v", notif.calls)
	}
}

func TestPoller_RetriesNotifyAfterFailure(t *testing.T) {
	ch := &mockChecker{fn: func(int) (string, error) { return "2026-07-01", nil }}
	notif := &recordNotifier{failNext: true}
	p, s := newPollerWith(t, ch, notif)
	w := seededWatch(t, s, "2026-09-01")

	_ = p.RunOnce(context.Background()) // notify fails -> guard not advanced
	got, _ := s.Get(context.Background(), w.ID)
	if got.LastNotifiedDate == nil || *got.LastNotifiedDate != "2026-09-01" {
		t.Errorf("guard must not advance on notify failure, got %v", got.LastNotifiedDate)
	}

	_ = p.RunOnce(context.Background()) // retries, succeeds
	if len(notif.calls) != 2 {
		t.Errorf("expected a retry (2 calls), got %v", notif.calls)
	}
	got, _ = s.Get(context.Background(), w.ID)
	if *got.LastNotifiedDate != "2026-07-01" {
		t.Errorf("guard must advance after successful retry, got %v", *got.LastNotifiedDate)
	}
}

func TestPoller_CancelDuringCheckSuppressesNotify(t *testing.T) {
	notif := &recordNotifier{}
	s := newTestStore(t)
	w := seededWatch(t, s, "2026-09-01")

	// The checker cancels the watch mid-tick, then reports an earlier date.
	ch := &mockChecker{fn: func(int) (string, error) {
		_ = s.Delete(context.Background(), w.ID)
		return "2026-07-01", nil
	}}
	p := NewPoller(s, ch, []Notifier{notif}, quietLogger())

	_ = p.RunOnce(context.Background())

	if len(notif.calls) != 0 {
		t.Errorf("cancelled watch must not notify, got %v", notif.calls)
	}
}
