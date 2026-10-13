package api

import (
	"context"
	"encoding/json"
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
