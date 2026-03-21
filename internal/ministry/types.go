package ministry

import "encoding/json"

// HUnit represents a single Health Unit returned by the /rv/searchhunits endpoint.
// It maps the exact JSON fields returned by the Hellenic API.
type HUnit struct {
	HUnitID   json.RawMessage `json:"hunitId"`
	HUnit     *int            `json:"hunit"`
	HUnitType *int            `json:"hunittype"`
	Name      string          `json:"name"`
	City      string          `json:"city"`
	Latitude  float64         `json:"lattitude"` // Notice the typo in the API
	Longitude float64         `json:"longitude"`
	ForeasID  int             `json:"-"` // We inject this manually later
}

// Specialty represents a medical specialty metadata as returned by /gen/getspecialities.
type Specialty struct {
	ID   int    `json:"speciality"`
	Name string `json:"name"`
}

// SearchPayload represents the outgoing JSON body required by /rv/searchhunits
// The Ministry API uses a mix of camelCase and some uppercase IDs.
type SearchPayload struct {
	StartDate    string `json:"startDate"`
	EndDate      string `json:"endDate"`
	PrefectureID *int   `json:"prefectureID"`
	SpecialityID int    `json:"specialityID"`
	ForeasID     int    `json:"foreasID"`
	HUnit        *int   `json:"hunit"`
	CDoorID      *int   `json:"cDoorId"`
	IsCovid      int    `json:"isCovid"`
	IsOnlyFd     int    `json:"isOnlyFd"`
	IsMachine    int    `json:"isMachine"`
}
