package ministry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSlotsInitPayload_JSONKeys(t *testing.T) {
	hId := 21
	payload := SlotsInitPayload{
		StartDate:    "2026-03-23T00:00:00.000Z",
		EndDate:      "2026-03-29T23:59:59.000Z",
		SpecialityID: 6,
		HUnit:        &hId,
		ForeasID:     1,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	out := string(data)
	// Assertions based on Ministry API requirements
	keys := []string{"startDate", "specialityID", "hunit", "foreasID"}
	for _, k := range keys {
		if !strings.Contains(out, "\""+k+"\"") {
			t.Errorf("JSON missing expected key: %s (got: %s)", k, out)
		}
	}
}

func TestSlotGroup_Unmarshal(t *testing.T) {
	raw := `{"groupColor": "disabled", "groupTitle": "Full"}`
	var g SlotGroup
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if g.GroupColor != "disabled" {
		t.Errorf("Expected groupColor 'disabled', got '%s'", g.GroupColor)
	}
}
