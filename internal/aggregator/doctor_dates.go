package aggregator

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// DoctorWithDate is a private doctor plus the first free date the Ministry reports
// for them in the search window. ScanOK=false means we don't know (upstream error or
// no ΑΜΚΑ to ask with), which is not the same as "no free slots".
type DoctorWithDate struct {
	ministry.Doctor
	FirstDate *string `json:"firstDate"`
	ScanOK    bool    `json:"scanOk"`
}

// doctorDateTTL keeps a page of doctors cheap to re-open without serving a date that
// has long been booked.
const doctorDateTTL = 15 * time.Minute

type doctorDateEntry struct {
	date    *string
	expires time.Time
}

type doctorDateCache struct {
	mu sync.Mutex
	m  map[string]doctorDateEntry
}

func (c *doctorDateCache) get(key string, now time.Time) (*string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.After(e.expires) {
		return nil, false
	}
	return e.date, true
}

func (c *doctorDateCache) put(key string, date *string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]doctorDateEntry{}
	}
	for k, e := range c.m { // bounded by the number of doctors; sweep expired on write
		if now.After(e.expires) {
			delete(c.m, k)
		}
	}
	c.m[key] = doctorDateEntry{date: date, expires: now.Add(doctorDateTTL)}
}

// DoctorFirstDates asks /rv/firstavailableslot, scoped by i_amka, for each doctor's
// first free date. Same probe the unit search runs per hospital; at most 5 at once,
// and only successful answers are cached so a failure is retried next time.
func (a *Aggregator) DoctorFirstDates(ctx context.Context, docs []ministry.Doctor, base ministry.SearchPayload) []DoctorWithDate {
	out := make([]DoctorWithDate, len(docs))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for i, d := range docs {
		out[i].Doctor = d
		if d.Amka == "" {
			continue
		}
		pref := "-" // no prefecture filter is its own scope, not prefecture 0
		if base.PrefectureID != nil {
			pref = strconv.Itoa(*base.PrefectureID)
		}
		key := d.Amka + "|" + strconv.Itoa(base.SpecialityID) + "|" + strconv.Itoa(base.ForeasID) + "|" + pref + "|" + day(base.StartDate) + "|" + day(base.EndDate)
		if date, ok := a.docDates.get(key, time.Now()); ok {
			out[i].FirstDate, out[i].ScanOK = date, true
			continue
		}
		wg.Add(1)
		go func(i int, amka, key string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p := base
			p.HUnit = nil
			p.IAmka = &amka
			dateStr, err := a.client.FirstAvailableSlot(ctx, p)
			if err != nil {
				a.logger.Debug("doctor first slot probe failed", "error", err)
				return
			}
			var date *string
			if len(dateStr) == 10 {
				date = &dateStr
			}
			out[i].FirstDate, out[i].ScanOK = date, true
			a.docDates.put(key, date, time.Now())
		}(i, d.Amka, key)
	}
	wg.Wait()
	return out
}

// day keys the cache by date, so "now" moving by a second doesn't miss it.
func day(ts string) string { return ts[:min(10, len(ts))] }
