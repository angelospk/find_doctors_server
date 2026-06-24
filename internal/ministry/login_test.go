package ministry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Simulates the gsis OAuth + finddoctors code-exchange + ΑΜΚΑ identification on one
// httptest server, and asserts LoginTaxisnet drives it browserless and harvests ALL
// THREE finddoctors cookies — including JSESSIONID, which is scoped to
// Path=/p-appointment (the bug that made data calls 403 when harvested at "/").
func TestLoginTaxisnet_BrowserlessFlow(t *testing.T) {
	mux := http.NewServeMux()

	// gsis: authorize → login form.
	mux.HandleFunc("/oauth2server/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.SetCookie(w, &http.Cookie{Name: "gsisJSESSIONID", Value: "g1", Path: "/oauth2server"})
			// After login the jar holds the gsis cookie; auto-approve straight to the
			// finddoctors redirect_uri with a code (no separate consent needed here).
			if _, err := r.Cookie("gsisAuth"); err == nil {
				http.Redirect(w, r, fdBaseURL+"/p-appointment/login/oauth2/code/taxisnet/?code=XYZ", http.StatusFound)
				return
			}
			_, _ = w.Write([]byte(`<form action="/oauth2server/j_spring_security_check"><input name="j_username"></form>`))
			return
		}
		// approval POST fallback
		http.Redirect(w, r, fdBaseURL+"/p-appointment/login/oauth2/code/taxisnet/?code=XYZ", http.StatusFound)
	})
	mux.HandleFunc("/oauth2server/j_spring_security_check", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("j_username") == "" || r.FormValue("j_password") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "gsisAuth", Value: "ok", Path: "/oauth2server"})
		http.Redirect(w, r, authorizeURL(), http.StatusFound)
	})

	// finddoctors: code exchange sets the 3 cookies (JSESSIONID at /p-appointment).
	mux.HandleFunc("/p-appointment/login/oauth2/code/taxisnet/", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "fd-sess", Path: "/p-appointment"})
		http.SetCookie(w, &http.Cookie{Name: "FindDoc", Value: "fd1", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "cookiesession1", Value: "cs1", Path: "/"})
		http.Redirect(w, r, fdBaseURL+"/p-appointment/", http.StatusFound)
	})
	mux.HandleFunc("/p-appointment/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/p-appointment/api/v1/auth/taxisnet-session", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"afm":"00130846996"}`))
	})
	var checkamkaCookie string
	mux.HandleFunc("/p-appointment/api/v1/patienterv/checkamka", func(w http.ResponseWriter, r *http.Request) {
		checkamkaCookie = r.Header.Get("Cookie")
		// checkamka must see JSESSIONID (the identification-bound cookie).
		if !strings.Contains(checkamkaCookie, "JSESSIONID=fd-sess") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"fdpatients":1423686,"a_amka":"04069601211"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Point both origins at the test server.
	oldGsis, oldFd := gsisBaseURL, fdBaseURL
	gsisBaseURL, fdBaseURL = srv.URL, srv.URL
	defer func() { gsisBaseURL, fdBaseURL = oldGsis, oldFd }()

	c := NewClient(srv.URL)
	if err := c.LoginTaxisnet(context.Background(), "user", "pass", "04069601211"); err != nil {
		t.Fatalf("LoginTaxisnet failed: %v", err)
	}

	got := c.SessionCookie()
	for _, name := range []string{"JSESSIONID=fd-sess", "FindDoc=fd1", "cookiesession1=cs1"} {
		if !strings.Contains(got, name) {
			t.Errorf("harvested session cookie missing %q; got %q", name, got)
		}
	}
	if !strings.Contains(checkamkaCookie, "JSESSIONID=fd-sess") {
		t.Errorf("checkamka did not receive JSESSIONID; got %q", checkamkaCookie)
	}
}

func TestAutoLoginEnabled(t *testing.T) {
	c := NewClient("https://example.test")
	if c.AutoLoginEnabled() {
		t.Fatal("expected auto-login disabled by default")
	}
	c.EnableAutoLogin("u", "p", "a")
	if !c.AutoLoginEnabled() {
		t.Fatal("expected auto-login enabled after EnableAutoLogin")
	}
}
