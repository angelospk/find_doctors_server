package ministry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Client handles communication with the Hellenic Ministry API.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient creates a new Ministry API client.
// baseURL is typically "https://www.finddoctors.gov.gr"
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{},
	}
}

// SearchHUnits queries the /rv/searchhunits endpoint to find hospitals or PFYs.
func (c *Client) SearchHUnits(ctx context.Context, payload SearchPayload) ([]HUnit, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/rv/searchhunits", c.BaseURL)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal search payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// The API refuses requests without certain headers.
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "no-auth")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://www.finddoctors.gov.gr")
	req.Header.Set("Referer", "https://www.finddoctors.gov.gr/p-appointment/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	var units []HUnit
	if err := json.NewDecoder(res.Body).Decode(&units); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// The Ministry API doesn't return the ForeasID in the response items,
	// so we inject it here to preserve the context of where this unit came from.
	for i := range units {
		units[i].ForeasID = payload.ForeasID
	}

	return units, nil
}

// FirstAvailableSlot queries the /rv/firstavailableslot endpoint to find the earliest appointment date.
func (c *Client) FirstAvailableSlot(ctx context.Context, payload SearchPayload) (string, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/rv/firstavailableslot", c.BaseURL)

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal search payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "no-auth")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://www.finddoctors.gov.gr")
	req.Header.Set("Referer", "https://www.finddoctors.gov.gr/p-appointment/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	// Response is just a raw string, e.g., "2026-05-07"
	var dateStr string
	if err := json.NewDecoder(res.Body).Decode(&dateStr); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return dateStr, nil
}

// GetSpecialties retrieves all medical specialties metadata.
func (c *Client) GetSpecialties(ctx context.Context) ([]Specialty, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/gen/getspecialities", c.BaseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "no-auth")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	var specs []Specialty
	if err := json.NewDecoder(res.Body).Decode(&specs); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return specs, nil
}

// GetSlotsInit retrieves the capacity calendar for a specific unit and specialty.
func (c *Client) GetSlotsInit(ctx context.Context, payload SlotsInitPayload) ([]SlotGroup, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/rv/getslotsinit", c.BaseURL)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "no-auth")
	req.Header.Set("Origin", "https://www.finddoctors.gov.gr")
	req.Header.Set("Referer", "https://www.finddoctors.gov.gr/p-appointment/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	var groups []SlotGroup
	if err := json.NewDecoder(res.Body).Decode(&groups); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return groups, nil
}

// GetActualSlots retrieves specific appointment times for a chosen day and group.
func (c *Client) GetActualSlots(ctx context.Context, payload GetActualSlotsPayload) ([]ActualSlot, error) {
	url := fmt.Sprintf("%s/p-appointment/api/v1/rv/getactualslots", c.BaseURL)

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "no-auth")
	req.Header.Set("Origin", "https://www.finddoctors.gov.gr")
	req.Header.Set("Referer", "https://www.finddoctors.gov.gr/p-appointment/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

	res, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	var slots []ActualSlot
	if err := json.NewDecoder(res.Body).Decode(&slots); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return slots, nil
}
