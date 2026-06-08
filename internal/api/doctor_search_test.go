package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/angelospk/find_doctors_server/internal/aggregator"
	"github.com/angelospk/find_doctors_server/internal/ministry"
)

// stubDoctorClient captures the payloads passed to doctor-search methods so
// tests can assert StartDate/EndDate are populated.
type stubDoctorClient struct {
	lastSearch ministry.SearchDoctorsPayload
}

func (s *stubDoctorClient) SearchDoctors(_ context.Context, p ministry.SearchDoctorsPayload) ([]ministry.Doctor, error) {
	s.lastSearch = p
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
