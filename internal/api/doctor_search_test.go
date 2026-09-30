package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// stubDoctorClient captures the payloads passed to doctor-search methods so
// tests can assert StartDate/EndDate are populated.
type stubDoctorClient struct {
	lastSearch ministry.SearchDoctorsPayload
	docs       []ministry.Doctor
}

func (s *stubDoctorClient) SearchDoctors(_ context.Context, p ministry.SearchDoctorsPayload) ([]ministry.Doctor, error) {
	s.lastSearch = p
	if s.docs != nil {
		return s.docs, nil
	}
	return []ministry.Doctor{}, nil
}

func (s *stubDoctorClient) SearchDoctorsByLocation(_ context.Context, p ministry.SearchDoctorsPayload) ([]ministry.Doctor, error) {
	s.lastSearch = p
	return []ministry.Doctor{}, nil
}

func (s *stubDoctorClient) SearchDoctorsFD(_ context.Context, p ministry.SearchDoctorsPayload) ([]ministry.Doctor, error) {
	s.lastSearch = p
	return []ministry.Doctor{}, nil
}

func (s *stubDoctorClient) SearchHunitsFD(context.Context, ministry.SearchPayload) ([]ministry.HUnit, error) {
	return []ministry.HUnit{}, nil
}
func (s *stubDoctorClient) GetHealthUnitTypes(context.Context) ([]ministry.HealthUnitType, error) {
	return nil, nil
}
func (s *stubDoctorClient) GetPrefectures(context.Context) ([]ministry.Prefecture, error) {
	return nil, nil
}
func (s *stubDoctorClient) GetCovidPrefectures(context.Context) ([]ministry.Prefecture, error) {
	return nil, nil
}
func (s *stubDoctorClient) GetMentalHealthPrefectures(context.Context) ([]ministry.Prefecture, error) {
	return nil, nil
}
func (s *stubDoctorClient) GetClinicDoors(context.Context, int, int) ([]ministry.ClinicDoor, error) {
	return nil, nil
}
func (s *stubDoctorClient) GetMachineRvTypes(context.Context) ([]ministry.MachineRvType, error) {
	return nil, nil
}
func (s *stubDoctorClient) SearchHunitsMachines(context.Context, ministry.SearchPayload) ([]ministry.HUnit, error) {
	return nil, nil
}

func newServerWithDoctorStub(stub *stubDoctorClient) *Server {
	agg := aggregator.New(nil).WithDoctorClient(stub)
	return NewServer(agg)
}

func TestHandleDoctorSearch_SetsStartAndEndDate(t *testing.T) {
	stub := &stubDoctorClient{}
	server := newServerWithDoctorStub(stub)

	req, _ := http.NewRequest("GET", "/api/doctors/search?specialtyId=24&prefectureId=5&foreasId=19", nil)
	rr := httptest.NewRecorder()
	server.HandleDoctorSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if stub.lastSearch.StartDate == "" {
		t.Errorf("expected StartDate to be set, got empty")
	}
	if stub.lastSearch.EndDate == "" {
		t.Errorf("expected EndDate to be set, got empty")
	}
}

func TestHandleDoctorNearby_SetsStartAndEndDate(t *testing.T) {
	stub := &stubDoctorClient{}
	server := newServerWithDoctorStub(stub)

	req, _ := http.NewRequest("GET", "/api/doctors/nearby?specialtyId=24&lat=37.9&lon=23.7", nil)
	rr := httptest.NewRecorder()
	server.HandleDoctorNearby(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if stub.lastSearch.StartDate == "" || stub.lastSearch.EndDate == "" {
		t.Errorf("expected StartDate/EndDate to be set, got %q/%q", stub.lastSearch.StartDate, stub.lastSearch.EndDate)
	}
}

func TestHandleFamilyDoctorSearch_SetsStartAndEndDate(t *testing.T) {
	stub := &stubDoctorClient{}
	server := newServerWithDoctorStub(stub)

	req, _ := http.NewRequest("GET", "/api/family-doctors/search?specialtyId=24&prefectureId=5", nil)
	rr := httptest.NewRecorder()
	server.HandleFamilyDoctorSearch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if stub.lastSearch.StartDate == "" || stub.lastSearch.EndDate == "" {
		t.Errorf("expected StartDate/EndDate on FD payload, got %q/%q", stub.lastSearch.StartDate, stub.lastSearch.EndDate)
	}
}

// probeClient answers firstavailableslot per doctor, recording who was probed.
type probeClient struct {
	mu     sync.Mutex
	dates  map[string]string
	probed []string
}

func (p *probeClient) FirstAvailableSlot(_ context.Context, pl ministry.SearchPayload) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probed = append(p.probed, *pl.IAmka)
	return p.dates[*pl.IAmka], nil
}
func (p *probeClient) SearchHUnits(context.Context, ministry.SearchPayload) ([]ministry.HUnit, error) {
	return nil, nil
}
func (p *probeClient) GetSpecialties(context.Context) ([]ministry.Specialty, error) { return nil, nil }
func (p *probeClient) GetSlotsInit(context.Context, ministry.SlotsInitPayload) ([]ministry.SlotGroup, error) {
	return nil, nil
}
func (p *probeClient) GetActualSlots(context.Context, ministry.GetActualSlotsPayload) ([]ministry.ActualSlot, error) {
	return nil, nil
}

func TestHandleDoctorSearch_WithFirstDate(t *testing.T) {
	stub := &stubDoctorClient{docs: []ministry.Doctor{{Amka: "A", LastName: "X"}, {Amka: "B"}, {Amka: "C"}}}
	mc := &probeClient{dates: map[string]string{"A": "2026-09-28"}}
	server := NewServer(aggregator.New(mc).WithDoctorClient(stub))

	// Opt-in, and only the returned page is probed.
	req, _ := http.NewRequest("GET", "/api/doctors/search?specialtyId=13&foreasId=19&limit=2&withFirstDate=1", nil)
	rr := httptest.NewRecorder()
	server.HandleDoctorSearch(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body struct {
		Data []struct {
			Amka      string  `json:"amka"`
			FirstDate *string `json:"firstDate"`
			ScanOK    bool    `json:"scanOk"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 || body.Data[0].FirstDate == nil || *body.Data[0].FirstDate != "2026-09-28" || !body.Data[1].ScanOK {
		t.Errorf("unexpected page: %s", rr.Body.String())
	}
	if len(mc.probed) != 2 {
		t.Errorf("want 2 probes (page only), got %v", mc.probed)
	}

	// Without the flag: no probes at all.
	mc.probed = nil
	req, _ = http.NewRequest("GET", "/api/doctors/search?specialtyId=13&foreasId=19", nil)
	server.HandleDoctorSearch(httptest.NewRecorder(), req)
	if len(mc.probed) != 0 {
		t.Errorf("probed without withFirstDate: %v", mc.probed)
	}
}
