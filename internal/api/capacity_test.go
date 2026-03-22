package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleHospitalCapacity_InvalidID(t *testing.T) {
	server := NewServer(nil)
	req, _ := http.NewRequest("GET", "/api/hospitals/abc/capacity", nil)
	rr := httptest.NewRecorder()

	server.HandleHospitalCapacity(rr, req)

	if rr.Code != http.StatusNotFound && rr.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 or 404, got %d", rr.Code)
	}
}
