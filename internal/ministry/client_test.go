package ministry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_SearchHUnits(t *testing.T) {
	// 1. Mock the Ministry Server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p-appointment/api/v1/rv/searchhunits" {
			t.Fatalf("Unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("Expected POST, got %s", r.Method)
		}

		// Read the payload to check ForeasID
		var payload SearchPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Failed to decode payload: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		
		hId := 718
		// Return different mock data based on foreasID
		if payload.ForeasID == 1 {
			json.NewEncoder(w).Encode([]HUnit{
				{HUnit: &hId, Name: "ΠΑΝΕΠΙΣΤΗΜΙΑΚΟ ΓΕΝΙΚΟ ΝΟΣΟΚΟΜΕΙΟ", City: "ΑΛΕΞΑΝΔΡΟΥΠΟΛΗ"},
			})
		} else if payload.ForeasID == 18 {
			json.NewEncoder(w).Encode([]HUnit{
				{HUnit: &hId, Name: "ΙΑΤΡΕΙΑ ΔΙΔΥΜΟΤΕΙΧΟΥ", City: "ΔΙΔΥΜΟΤΕΙΧΟ"},
			})
		} else {
			// Simulating the actual ministry API empty/PFY fallback
			json.NewEncoder(w).Encode([]HUnit{})
		}
	}))
	defer mockServer.Close()

	// 2. Initialize our Client connected to the Mock Server
	client := NewClient(mockServer.URL)

	// 3. Test Foreas 1 (Hospitals)
	pref := 11
	payload := SearchPayload{
		PrefectureID: &pref,
		SpecialityID: 6,
		ForeasID:     1,
	}
	
	units, err := client.SearchHUnits(context.Background(), payload)
	if err != nil {
		t.Fatalf("SearchHUnits failed: %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("Expected 1 unit, got %d", len(units))
	}
	if units[0].Name != "ΠΑΝΕΠΙΣΤΗΜΙΑΚΟ ΓΕΝΙΚΟ ΝΟΣΟΚΟΜΕΙΟ" {
		t.Errorf("Unexpected hospital name: %s", units[0].Name)
	}

	// 4. Test Foreas 18 (PFY)
	payload.ForeasID = 18
	unitsPFY, err := client.SearchHUnits(context.Background(), payload)
	if err != nil {
		t.Fatalf("SearchHUnits failed: %v", err)
	}
	if len(unitsPFY) != 1 {
		t.Fatalf("Expected 1 unit, got %d", len(unitsPFY))
	}
	if unitsPFY[0].Name != "ΙΑΤΡΕΙΑ ΔΙΔΥΜΟΤΕΙΧΟΥ" {
		t.Errorf("Unexpected PFY name: %s", unitsPFY[0].Name)
	}
}

func TestClient_FirstAvailableSlot(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p-appointment/api/v1/rv/firstavailableslot" {
			t.Fatalf("Unexpected path: %s", r.URL.Path)
		}
		var payload SearchPayload
		json.NewDecoder(r.Body).Decode(&payload)

		h := 0
		if payload.HUnit != nil {
			h = *payload.HUnit
		}
		
		if h == 718 {
			w.Write([]byte(`"2026-05-07"`))
		} else {
			// Simulating PFY returning no slots
			w.Write([]byte(`""`))
		}
	}))
	defer mockServer.Close()

	client := NewClient(mockServer.URL)
	payload := SearchPayload{HUnit: new(int)}
	*payload.HUnit = 718

	dateStr, err := client.FirstAvailableSlot(context.Background(), payload)
	if err != nil {
		t.Fatalf("FirstAvailableSlot failed: %v", err)
	}
	if dateStr != "2026-05-07" {
		t.Errorf("Expected 2026-05-07, got %s", dateStr)
	}

	*payload.HUnit = 999 // Unknown unit
	dateStrEmpty, err := client.FirstAvailableSlot(context.Background(), payload)
	if err != nil {
		t.Fatalf("FirstAvailableSlot failed: %v", err)
	}
	if dateStrEmpty != "" {
		t.Errorf("Expected empty string, got %s", dateStrEmpty)
	}
}

func TestClient_GetSpecialties(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"speciality": 22, "name": "ΑΓΓΕΙΟΧΕΙΡΟΥΡΓΟΣ"}]`))
	}))
	defer mockServer.Close()

	client := NewClient(mockServer.URL)
	specs, err := client.GetSpecialties(context.Background())
	if err != nil {
		t.Fatalf("GetSpecialties failed: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != 22 {
		t.Errorf("Expected spec ID 22, got %v", specs)
	}
}
