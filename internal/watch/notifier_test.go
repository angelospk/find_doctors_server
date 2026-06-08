package watch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func strp(s string) *string { return &s }

func TestWebhookNotifier_PostsPayload(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.Client())
	w := sampleWatch()
	w.ID = "abc"
	w.WebhookURL = strp(srv.URL)
	w.LastNotifiedDate = strp("2026-08-01")

	attempted, err := n.Notify(context.Background(), w, "2026-07-01")
	if !attempted || err != nil {
		t.Fatalf("expected attempted+success, got attempted=%v err=%v", attempted, err)
	}
	if gotBody["watchId"] != "abc" || gotBody["newDate"] != "2026-07-01" || gotBody["previousDate"] != "2026-08-01" {
		t.Errorf("unexpected payload: %+v", gotBody)
	}
}

func TestWebhookNotifier_SkipsWhenNoURL(t *testing.T) {
	n := NewWebhookNotifier(http.DefaultClient)
	attempted, err := n.Notify(context.Background(), sampleWatch(), "2026-07-01")
	if attempted || err != nil {
		t.Errorf("expected skip, got attempted=%v err=%v", attempted, err)
	}
}

func TestWebhookNotifier_Non2xxIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := NewWebhookNotifier(srv.Client())
	w := sampleWatch()
	w.WebhookURL = strp(srv.URL)

	attempted, err := n.Notify(context.Background(), w, "2026-07-01")
	if !attempted || err == nil {
		t.Errorf("expected attempted with error, got attempted=%v err=%v", attempted, err)
	}
}

func TestTelegramNotifier_SendsMessage(t *testing.T) {
	var path string
	var form map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &form)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	n := NewTelegramNotifier("TOK123", srv.Client())
	n.BaseURL = srv.URL
	w := sampleWatch()
	w.TelegramChatID = strp("999")

	attempted, err := n.Notify(context.Background(), w, "2026-07-01")
	if !attempted || err != nil {
		t.Fatalf("expected attempted+success, got attempted=%v err=%v", attempted, err)
	}
	if !strings.Contains(path, "botTOK123") || !strings.Contains(path, "sendMessage") {
		t.Errorf("unexpected telegram path: %s", path)
	}
	if form["chat_id"] != "999" {
		t.Errorf("expected chat_id 999, got %v", form["chat_id"])
	}
	if txt, _ := form["text"].(string); !strings.Contains(txt, "2026-07-01") {
		t.Errorf("expected date in message text, got %q", txt)
	}
}

func TestTelegramNotifier_SkipsWhenNoChatID(t *testing.T) {
	n := NewTelegramNotifier("TOK", http.DefaultClient)
	attempted, err := n.Notify(context.Background(), sampleWatch(), "2026-07-01")
	if attempted || err != nil {
		t.Errorf("expected skip, got attempted=%v err=%v", attempted, err)
	}
}
