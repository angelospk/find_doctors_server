package ministry

import "encoding/json"

// HUnit represents a single Health Unit returned by the /rv/searchhunits endpoint.
type HUnit struct {
	HUnitID      json.RawMessage `json:"hunitId"`
	HUnit        *int            `json:"hunit"`
	HUnitType    *int            `json:"hunittype"`
	Name         string          `json:"name"`
	City         string          `json:"city"`
	Zip          string          `json:"zip"`
	Phone1       string          `json:"phone1"`
	Phone2       string          `json:"phone2"`
	Address      string          `json:"address"`
	Latitude     float64         `json:"lattitude"`
	Longitude    float64         `json:"longitude"`
	ForeasID     int             `json:"foreasId"`
	Region       *int            `json:"region"`
	Prefecture   *int            `json:"prefecture"`
	IsActive     *int            `json:"isactive"`
	Clinics      []interface{}   `json:"clinics"`
	ResponseCode int             `json:"responseCode"`
}

// Specialty represents a medical specialty metadata.
type Specialty struct {
	ID   int    `json:"speciality"`
	Name string `json:"name"`
}

// Slot represents an available appointment time.
type Slot struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	IsFree    bool   `json:"isFree"`
}

// DaySlots represents a day's worth of slots for a unit.
type DaySlots struct {
	Date  string `json:"date"`
	Slots []Slot `json:"slots"`
}

// SearchPayload represents the outgoing JSON body for searches and availability.
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

// SlotsInitPayload represents the request body for /rv/getslotsinit.
type SlotsInitPayload struct {
	StartDate    string `json:"startDate"`
	EndDate      string `json:"endDate"`
	SpecialityID int    `json:"specialityID"`
	PrefectureID *int   `json:"prefectureID"`
	HUnit        *int   `json:"hunit"`
	ForeasID     int    `json:"foreasID"`
	IsCovid      int    `json:"isCovid"`
	IsOnlyFd     int    `json:"isOnlyFd"`
	IsMachine    int    `json:"isMachine"`
}

// SlotGroup represents a capacity block (e.g., 3 hours) in the /rv/getslotsinit response.
type SlotGroup struct {
	GroupColor   string `json:"groupColor"`
	GroupTitle   string `json:"groupTitle"`
	ResponseCode int    `json:"responseCode"`
}
