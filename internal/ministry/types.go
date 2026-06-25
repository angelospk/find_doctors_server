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
	StartDate      string `json:"startDate"`
	EndDate        string `json:"endDate"`
	PrefectureID   *int   `json:"prefectureID"`
	SpecialityID   int    `json:"specialityID"`
	ForeasID       int    `json:"foreasID"`
	HUnit          *int   `json:"hunit"`
	CDoorID        *int   `json:"cDoorId"`
	IsCovid        int    `json:"isCovid"`
	IsOnlyFd       int    `json:"isOnlyFd"`
	IsMachine      int    `json:"isMachine"`
	IsMentalHealth int    `json:"isMentalHealth,omitempty"`
	RvTypeID       int    `json:"rvtypeId,omitempty"`
	// IAmka identifies a private ΕΟΠΥΥ doctor (foreasID 19) for slot lookups; omitted for units.
	IAmka *string `json:"i_amka,omitempty"`
}

// SlotsInitPayload represents the request body for /rv/getslotsinit.
type SlotsInitPayload struct {
	StartDate      string `json:"startDate"`
	EndDate        string `json:"endDate"`
	SpecialityID   int    `json:"specialityID"`
	PrefectureID   *int   `json:"prefectureID"`
	HUnit          *int   `json:"hunit,omitempty"`
	ForeasID       int    `json:"foreasID"`
	CDoorID        *int   `json:"cDoorId,omitempty"`
	IsCovid        int    `json:"isCovid"`
	IsOnlyFd       int    `json:"isOnlyFd"`
	IsMachine      int    `json:"isMachine"`
	IsMentalHealth int    `json:"isMentalHealth,omitempty"`
	RvTypeID       int    `json:"rvtypeId,omitempty"`
	// IAmka identifies a private ΕΟΠΥΥ doctor (foreasID 19); omitted for units.
	IAmka *string `json:"i_amka,omitempty"`
}

// SlotGroup represents a capacity block (e.g., 3 hours) in the /rv/getslotsinit response.
type SlotGroup struct {
	Day          int    `json:"day"`
	GroupID      int    `json:"groupId"`
	GroupColor   string `json:"groupColor"`
	GroupName    string `json:"groupName"`
	ResponseCode int    `json:"responseCode"`
}

// ActualSlot represents a specific, granular appointment returned by /rv/getactualslots.
// tygo:generate
type ActualSlot struct {
	HUnitID   int     `json:"hUnitId"`
	RVDate    string  `json:"rvDate"`
	RVTime    string  `json:"rvtime"`
	DocName   string  `json:"doc_name"`
	Address   string  `json:"address"`
	City      string  `json:"city"`
	Comments  *string `json:"comments"`
	Comments2 *string `json:"comments2"`
	RVTName   string  `json:"rvtname"`
}

// GetActualSlotsPayload represents the request body for /rv/getactualslots.
// NOTE: this endpoint uses different key casing than firstavailableslot/getslotsinit
// (lowercase-d prefectureId/specialityId, key `foreas`); wrong casing returns HTTP 500.
type GetActualSlotsPayload struct {
	Day          int    `json:"day"`
	DDate        string `json:"ddate"`
	GroupID      int    `json:"groupId"`
	HUnit        *int   `json:"hunit,omitempty"`
	Foreas       int    `json:"foreas"`
	SpecialityID int    `json:"specialityId"`
	PrefectureID int    `json:"prefectureId"`
	IsOnlyFd     int    `json:"isOnlyFd"`
	IsMachine    int    `json:"isMachine"`
	CDoorID      *int   `json:"cDoorId"`
	// IAmka identifies a private ΕΟΠΥΥ doctor (foreasID 19); omitted for units.
	IAmka *string `json:"i_amka,omitempty"`
}

// HealthUnitType represents a foreas/unit-type metadata entry from /gen/getHealthUnitTypes/.
// tygo:generate
type HealthUnitType struct {
	HUnitType int    `json:"hUnitType"`
	Name      string `json:"name"`
	IsActive  int    `json:"isActive"`
}

// Prefecture represents a Greek prefecture from /gen/getprefectures.
// tygo:generate
type Prefecture struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	ResponseCode int    `json:"responseCode,omitempty"`
}

// Doctor represents a named physician returned by /rv/searchdoctors.
// tygo:generate
type Doctor struct {
	FirstName     string   `json:"firstName"`
	LastName      string   `json:"lastName"`
	Amka          string   `json:"amka"`
	SpecialtyID   *int     `json:"specialtyId"`
	SpecialtyName string   `json:"specialtyName"`
	Address       string   `json:"address"`
	CityName      string   `json:"cityName"`
	Zip           string   `json:"zip"`
	Phone         *string  `json:"phone"`
	Latitude      float64  `json:"lattitude"`
	Longitude     float64  `json:"longitude"`
	Distance      *float64 `json:"distance"`
	ForeasID      int      `json:"foreasId"`
	ResponseCode  int      `json:"responseCode"`
}

// SearchDoctorsPayload is the JSON body for /rv/searchdoctors and variants.
type SearchDoctorsPayload struct {
	StartDate      string   `json:"startDate"`
	EndDate        string   `json:"endDate"`
	PrefectureID   *int     `json:"prefectureID"`
	SpecialityID   int      `json:"specialityID"`
	ForeasID       int      `json:"foreasID"`
	Latitude       *float64 `json:"lattitude,omitempty"`
	Longitude      *float64 `json:"longitude,omitempty"`
	Distance       *float64 `json:"distance,omitempty"`
	IsOnlyFd       int      `json:"isOnlyFd"`
	IsCovid        int      `json:"isCovid"`
	IsMachine      int      `json:"isMachine"`
	IsMentalHealth int      `json:"isMentalHealth,omitempty"`
	RvTypeID       int      `json:"rvtypeId,omitempty"`
	FirstName      string   `json:"firstName,omitempty"`
	LastName       string   `json:"lastName,omitempty"`
}

// ClinicDoor represents an entry from /gen/cdoorsbyhunitspeciality.
// tygo:generate
type ClinicDoor struct {
	CDoorID int    `json:"cDoorId"`
	Name    string `json:"name"`
}

// MachineRvType describes a diagnostic-machine RV type entry.
// tygo:generate
type MachineRvType struct {
	RvTypeID  int    `json:"rvTypeId"`
	Name      string `json:"name"`
	PayType   int    `json:"payType"`
	IsMachine int    `json:"isMachine"`
}
