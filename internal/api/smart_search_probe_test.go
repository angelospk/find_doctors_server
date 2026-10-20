package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// manyUnitsClient returns n active units (foreas 1 only) with no coordinates.
type manyUnitsClient struct {
	stubSlotClient
	n int
}

func (m *manyUnitsClient) SearchHUnits(_ context.Context, p ministry.SearchPayload) ([]ministry.HUnit, error) {
	if p.ForeasID != 1 {
		return nil, nil
	}
	active := 1
	out := make([]ministry.HUnit, m.n)
	for i := range out {
		h := i + 1
		out[i] = ministry.HUnit{HUnitID: json.RawMessage(`1`), HUnit: &h, Name: "U", ForeasID: 1, IsActive: &active}
	}
	return out, nil
}

func TestHandleSmartSearch_ProbeLimitAndMeta(t *testing.T) {
	server := NewServer(aggregator.New(&manyUnitsClient{n: 5})).WithUnitProbeLimit(3)
	req, _ := http.NewRequest("GET", "/api/search?specialtyId=9&limit=100", nil)
	rr := httptest.NewRecorder()
	server.HandleSmartSearch(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Data []json.RawMessage `json:"data"`
		Meta struct {
			Total      int `json:"total"`
			Candidates int `json:"candidates"`
			Probed     int `json:"probed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Meta.Candidates != 5 || body.Meta.Probed != 3 || len(body.Data) != 3 || body.Meta.Total != 3 {
		t.Fatalf("meta %+v data %d, want 5 candidates, 3 probed and returned", body.Meta, len(body.Data))
	}
}

func TestNewServerDefaultsToSixtyProbes(t *testing.T) {
	if got := NewServer(aggregator.New(&stubSlotClient{})).unitProbeLimit; got != 60 {
		t.Fatalf("default unit probe limit = %d, want 60", got)
	}
}

// The default window starts at the current quarter hour, not the current millisecond:
// the unit-date cache keys the exact window, and "now" would never repeat.
func TestDefaultWindowStartIsTheQuarterHour(t *testing.T) {
	stub := &stubSlotClient{}
	server := NewServer(aggregator.New(stub))
	req, _ := http.NewRequest("GET", "/api/search?specialtyId=9", nil)
	server.HandleSmartSearch(httptest.NewRecorder(), req)
	got := stub.lastPayload().StartDate
	ts, err := time.Parse("2006-01-02T15:04:05.000Z", got)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Minute()%15 != 0 || ts.Second() != 0 || ts.Nanosecond() != 0 {
		t.Fatalf("StartDate %s is not on a quarter hour", got)
	}
}

type probeMeta struct {
	Total      int  `json:"total"`
	Candidates int  `json:"candidates"`
	Probed     int  `json:"probed"`
	Truncated  bool `json:"truncated"`
}

func smartSearch(t *testing.T, s *Server, query string) (int, probeMeta) {
	t.Helper()
	req, _ := http.NewRequest("GET", "/api/search?specialtyId=9&limit=100"+query, nil)
	rr := httptest.NewRecorder()
	s.HandleSmartSearch(rr, req)
	var body struct {
		Meta probeMeta `json:"meta"`
	}
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	return rr.Code, body.Meta
}

// The caller sets the cap per request: maxProbes=0 asks for every unit (the nationwide
// directory scan), maxProbes=n for at most n; without it the server default holds.
// meta.truncated says whether the cap dropped units.
func TestHandleSmartSearch_MaxProbesPerRequest(t *testing.T) {
	server := NewServer(aggregator.New(&manyUnitsClient{n: 5})).WithUnitProbeLimit(3)
	for _, tc := range []struct {
		query     string
		probed    int
		truncated bool
	}{
		{"", 3, true},
		{"&maxProbes=0", 5, false},
		{"&maxProbes=2", 2, true},
		{"&maxProbes=5", 5, false},
	} {
		code, m := smartSearch(t, server, tc.query)
		if code != http.StatusOK || m.Probed != tc.probed || m.Truncated != tc.truncated || m.Candidates != 5 {
			t.Fatalf("%q: status %d meta %+v, want probed %d truncated %v", tc.query, code, m, tc.probed, tc.truncated)
		}
	}
	for _, bad := range []string{"&maxProbes=-1", "&maxProbes=abc"} {
		if code, _ := smartSearch(t, server, bad); code != http.StatusBadRequest {
			t.Fatalf("%q: status %d, want 400", bad, code)
		}
	}
}

// A hard ceiling bounds every search, maxProbes=0 included: a public deployment cannot be
// asked to probe without limit. A search the ceiling cut says so.
func TestHandleSmartSearch_ProbeCeiling(t *testing.T) {
	server := NewServer(aggregator.New(&manyUnitsClient{n: 5})).WithUnitProbeLimit(3).WithUnitProbeMax(4)
	for _, q := range []string{"&maxProbes=0", "&maxProbes=10"} {
		code, m := smartSearch(t, server, q)
		if code != http.StatusOK || m.Probed != 4 || !m.Truncated {
			t.Fatalf("%q: status %d meta %+v, want 4 probed and truncated", q, code, m)
		}
	}
	if got := NewServer(aggregator.New(&stubSlotClient{})).unitProbeMax; got != 1000 {
		t.Fatalf("default ceiling %d, want 1000", got)
	}
}

type failingSectorClient struct{ manyUnitsClient }

func (f *failingSectorClient) SearchHUnits(ctx context.Context, p ministry.SearchPayload) ([]ministry.HUnit, error) {
	if p.ForeasID == 18 {
		return nil, errors.New("upstream 502")
	}
	return f.manyUnitsClient.SearchHUnits(ctx, p)
}

// One sector failing is not an error for the app (the other sector still answers), but
// meta.failedSectors tells a directory scan that the list is short.
func TestHandleSmartSearch_ReportsFailedSectors(t *testing.T) {
	ok := NewServer(aggregator.New(&manyUnitsClient{n: 2}))
	bad := NewServer(aggregator.New(&failingSectorClient{manyUnitsClient{n: 2}}))
	for _, tc := range []struct {
		s    *Server
		want int
	}{{ok, 0}, {bad, 1}} {
		req, _ := http.NewRequest("GET", "/api/search?specialtyId=9&maxProbes=0", nil)
		rr := httptest.NewRecorder()
		tc.s.HandleSmartSearch(rr, req)
		var body struct {
			Meta map[string]any `json:"meta"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if rr.Code != http.StatusOK || body.Meta["failedSectors"] != float64(tc.want) {
			t.Fatalf("status %d meta %v, want failedSectors %d", rr.Code, body.Meta, tc.want)
		}
	}
}
