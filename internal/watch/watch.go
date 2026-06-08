// Package watch implements the cancellation watchdog (#5): a registry of
// per-unit appointment watches, a background poller that re-checks the upstream
// first-available date, and notifiers that alert the user when an earlier slot
// appears.
package watch

import (
	"errors"
	"time"
)

// Status is the lifecycle state of a watch.
type Status string

const (
	StatusActive    Status = "active"
	StatusExpired   Status = "expired"
	StatusCancelled Status = "cancelled"
)

// ErrNotFound is returned by the Store when a watch ID is unknown.
var ErrNotFound = errors.New("watch: not found")

// Watch is a single per-unit appointment watch.
type Watch struct {
	ID           string `json:"id"`
	HUnitID      int    `json:"hunitId"`
	SpecialtyID  int    `json:"specialtyId"`
	ForeasID     int    `json:"foreasId"`
	PrefectureID *int   `json:"prefectureId,omitempty"`

	// CurrentDate is the latest observed first-available date (YYYY-MM-DD). It
	// may move later (slots fill) or earlier (a cancellation). nil until the
	// first successful observation.
	CurrentDate *string `json:"currentDate,omitempty"`
	// LastNotifiedDate is the earliest date the user has been successfully told
	// about; it is the dedup + retry guard.
	LastNotifiedDate *string `json:"lastNotifiedDate,omitempty"`

	TelegramChatID *string `json:"telegramChatId,omitempty"`
	WebhookURL     *string `json:"webhookUrl,omitempty"`

	Status        Status    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	LastCheckedAt time.Time `json:"lastCheckedAt,omitempty"`
}
