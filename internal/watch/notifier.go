package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Notifier delivers a watch alert through one channel.
//
// attempted is false when the channel isn't configured on the watch (the
// notifier simply skips it). When attempted is true, err reports whether
// delivery succeeded.
type Notifier interface {
	Notify(ctx context.Context, w Watch, newDate string) (attempted bool, err error)
}

// WebhookNotifier POSTs a JSON alert to the watch's WebhookURL.
type WebhookNotifier struct {
	client *http.Client
}

func NewWebhookNotifier(client *http.Client) *WebhookNotifier {
	return &WebhookNotifier{client: client}
}

func (n *WebhookNotifier) Notify(ctx context.Context, w Watch, newDate string) (bool, error) {
	if w.WebhookURL == nil || *w.WebhookURL == "" {
		return false, nil
	}
	var prev string
	if w.LastNotifiedDate != nil {
		prev = *w.LastNotifiedDate
	}
	body, err := json.Marshal(map[string]any{
		"watchId":      w.ID,
		"hunitId":      w.HUnitID,
		"specialtyId":  w.SpecialtyID,
		"newDate":      newDate,
		"previousDate": prev,
	})
	if err != nil {
		return true, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *w.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return true, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return true, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return true, fmt.Errorf("watch: webhook returned %d", resp.StatusCode)
	}
	return true, nil
}

// TelegramNotifier sends a message via the Telegram Bot API.
type TelegramNotifier struct {
	token   string
	client  *http.Client
	BaseURL string // overridable for tests; defaults to the public Bot API
}

func NewTelegramNotifier(token string, client *http.Client) *TelegramNotifier {
	return &TelegramNotifier{token: token, client: client, BaseURL: "https://api.telegram.org"}
}

func (n *TelegramNotifier) Notify(ctx context.Context, w Watch, newDate string) (bool, error) {
	if w.TelegramChatID == nil || *w.TelegramChatID == "" || n.token == "" {
		return false, nil
	}
	text := fmt.Sprintf("🔔 Βρέθηκε νωρίτερο ραντεβού: %s (μονάδα %d, ειδικότητα %d).", newDate, w.HUnitID, w.SpecialtyID)
	body, err := json.Marshal(map[string]any{
		"chat_id": *w.TelegramChatID,
		"text":    text,
	})
	if err != nil {
		return true, err
	}
	url := fmt.Sprintf("%s/bot%s/sendMessage", n.BaseURL, n.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return true, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return true, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return true, fmt.Errorf("watch: telegram returned %d", resp.StatusCode)
	}
	return true, nil
}
