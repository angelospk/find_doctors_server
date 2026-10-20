package ministry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Ministry answers 400 to specialityID; the web app sends specialtyId and iscovid19.
func TestClient_GetClinicDoorsQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p-appointment/api/v1/gen/cdoorsbyhunitspeciality" {
			t.Fatalf("path %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("hunitId") != "124" || q.Get("specialtyId") != "5" || q.Get("iscovid19") != "false" || q.Has("specialityID") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode([]ClinicDoor{{CDoorID: 7, Name: "Τακτικό"}})
	}))
	defer srv.Close()

	doors, err := NewClient(srv.URL).GetClinicDoors(context.Background(), 124, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(doors) != 1 || doors[0].CDoorID != 7 {
		t.Fatalf("doors %+v", doors)
	}
}
