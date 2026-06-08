package watch

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

var (
	pollFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "watch_poll_failures_total",
		Help: "Upstream first-available-slot checks that failed during a watch poll.",
	})
	notifyFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "watch_notify_failures_total",
		Help: "Notifier deliveries that failed.",
	})
)

const (
	defaultPollInterval = 5 * time.Minute
	defaultPerCall      = 5 * time.Second
)

// SlotChecker is the narrow upstream dependency the poller needs. The ministry
// client satisfies it directly, so polls go through the rate-limited client.
type SlotChecker interface {
	FirstAvailableSlot(ctx context.Context, payload ministry.SearchPayload) (string, error)
}

// Poller re-checks active watches and fires notifiers when an earlier date appears.
type Poller struct {
	store     *Store
	checker   SlotChecker
	notifiers []Notifier
	logger    *slog.Logger

	Interval time.Duration
	PerCall  time.Duration
}

func NewPoller(store *Store, checker SlotChecker, notifiers []Notifier, logger *slog.Logger) *Poller {
	if logger == nil {
		logger = slog.Default()
	}
	return &Poller{
		store:     store,
		checker:   checker,
		notifiers: notifiers,
		logger:    logger,
		Interval:  defaultPollInterval,
		PerCall:   defaultPerCall,
	}
}

// Run polls on a ticker until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.RunOnce(ctx); err != nil {
				p.logger.Warn("watch poll tick failed", "error", err)
			}
		}
	}
}

// RunOnce performs a single poll over all active watches. Per-watch failures are
// logged and metered, not surfaced.
func (p *Poller) RunOnce(ctx context.Context) error {
	active, err := p.store.ListActive(ctx)
	if err != nil {
		return err
	}
	for _, w := range active {
		p.checkOne(ctx, w)
	}
	return nil
}

func (p *Poller) checkOne(ctx context.Context, w Watch) {
	payload := ministry.SearchPayload{
		StartDate:    time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		EndDate:      time.Now().AddDate(0, 6, 0).UTC().Format("2006-01-02T15:04:05.000Z"),
		SpecialityID: w.SpecialtyID,
		ForeasID:     w.ForeasID,
		PrefectureID: w.PrefectureID,
		HUnit:        &w.HUnitID,
	}

	cctx, cancel := context.WithTimeout(ctx, p.PerCall)
	date, err := p.checker.FirstAvailableSlot(cctx, payload)
	cancel()
	if err != nil {
		pollFailures.Inc()
		p.logger.Warn("watch poll failed", "watch", w.ID, "error", err)
		return
	}
	if date == "" {
		return // no slot available; nothing to record
	}

	// Re-read state: the check may have raced with a Delete.
	cur, err := p.store.Get(ctx, w.ID)
	if err != nil || cur.Status != StatusActive {
		return
	}

	// First observation seeds the baseline without notifying.
	if cur.LastNotifiedDate == nil {
		_, _ = p.store.Apply(ctx, w.ID, func(w *Watch) bool {
			w.CurrentDate = &date
			w.LastNotifiedDate = &date
			w.LastCheckedAt = time.Now()
			return true
		})
		return
	}

	improvement := date < *cur.LastNotifiedDate
	notified := true
	if improvement {
		notified = p.notify(ctx, cur, date)
	}

	_, _ = p.store.Apply(ctx, w.ID, func(w *Watch) bool {
		w.CurrentDate = &date
		w.LastCheckedAt = time.Now()
		if improvement && notified {
			w.LastNotifiedDate = &date
		}
		return true
	})
}

// notify delivers to every notifier. It returns whether the guard may advance:
// true when no channel is configured (poll-only) or at least one channel
// delivered; false when channels were attempted and all failed.
func (p *Poller) notify(ctx context.Context, w Watch, date string) bool {
	attempted, succeeded := 0, 0
	for _, n := range p.notifiers {
		cctx, cancel := context.WithTimeout(ctx, p.PerCall)
		ok, err := n.Notify(cctx, w, date)
		cancel()
		if !ok {
			continue
		}
		attempted++
		if err != nil {
			notifyFailures.Inc()
			p.logger.Warn("watch notify failed", "watch", w.ID, "error", err)
		} else {
			succeeded++
		}
	}
	if attempted == 0 {
		return true // poll-only watch
	}
	return succeeded >= 1
}
