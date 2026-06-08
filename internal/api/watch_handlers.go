package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/angelospk/find_doctors_server/internal/watch"
)

// WithWatches wires the cancellation watchdog store and the seed checker used to
// capture the first-available date at create time. Returns the server.
func (s *Server) WithWatches(store *watch.Store, seeder watch.SlotChecker) *Server {
	s.watches = store
	s.watchSeeder = seeder
	return s
}

// validateWebhookURL guards against SSRF: the watchdog POSTs to this URL from the
// server, so it must be an http(s) URL whose host does not resolve to a loopback,
// private, link-local, or otherwise internal address (e.g. cloud metadata).
func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("webhookUrl must be an http(s) URL")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("webhookUrl must have a host")
	}

	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil || len(resolved) == 0 {
			return errors.New("webhookUrl host could not be resolved")
		}
		ips = resolved
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("webhookUrl must point to a public host")
		}
	}
	return nil
}

type createWatchRequest struct {
	HUnitID        int     `json:"hunitId"`
	SpecialtyID    int     `json:"specialtyId"`
	ForeasID       int     `json:"foreasId"`
	PrefectureID   *int    `json:"prefectureId"`
	TelegramChatID *string `json:"telegramChatId"`
	WebhookURL     *string `json:"webhookUrl"`
	ExpiresInDays  int     `json:"expiresInDays"`
}

const (
	watchDefaultDays = 14
	watchMaxDays     = 30
)

// HandleCreateWatch registers a per-unit watch and seeds its baseline date.
func (s *Server) HandleCreateWatch(w http.ResponseWriter, r *http.Request) {
	if s.watches == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "watchdog not enabled")
		return
	}
	var req createWatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if req.HUnitID == 0 || req.SpecialtyID == 0 || req.ForeasID == 0 {
		writeJSONError(w, http.StatusBadRequest, "missing_param", "hunitId, specialtyId and foreasId are required")
		return
	}
	if req.WebhookURL != nil && *req.WebhookURL != "" {
		if err := validateWebhookURL(*req.WebhookURL); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_param", err.Error())
			return
		}
	}

	days := req.ExpiresInDays
	if days <= 0 {
		days = watchDefaultDays
	}
	if days > watchMaxDays {
		days = watchMaxDays
	}

	wt := watch.Watch{
		HUnitID:        req.HUnitID,
		SpecialtyID:    req.SpecialtyID,
		ForeasID:       req.ForeasID,
		PrefectureID:   req.PrefectureID,
		TelegramChatID: req.TelegramChatID,
		WebhookURL:     req.WebhookURL,
		ExpiresAt:      time.Now().AddDate(0, 0, days),
	}

	// Synchronous seed: capture the current first-available date so a cancellation
	// arriving between now and the first poll isn't silently absorbed.
	if s.watchSeeder != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if date, err := s.watchSeeder.FirstAvailableSlot(ctx, watch.PayloadFor(wt)); err == nil && date != "" {
			wt.CurrentDate = &date
			wt.LastNotifiedDate = &date
		} else if err != nil {
			s.logger.Warn("watch seed poll failed", "error", err)
		}
	}

	created, err := s.watches.Create(r.Context(), wt)
	if err != nil {
		s.logger.Warn("watch create failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "store_error", "could not persist watch")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// HandleGetWatch returns a watch's current state.
func (s *Server) HandleGetWatch(w http.ResponseWriter, r *http.Request) {
	if s.watches == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "watchdog not enabled")
		return
	}
	got, err := s.watches.Get(r.Context(), r.PathValue("id"))
	if err == watch.ErrNotFound {
		writeJSONError(w, http.StatusNotFound, "not_found", "unknown watch id")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "store_error", "could not read watch")
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// HandleDeleteWatch cancels a watch. Idempotent.
func (s *Server) HandleDeleteWatch(w http.ResponseWriter, r *http.Request) {
	if s.watches == nil {
		writeJSONError(w, http.StatusNotImplemented, "feature_unavailable", "watchdog not enabled")
		return
	}
	if err := s.watches.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "store_error", "could not cancel watch")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
