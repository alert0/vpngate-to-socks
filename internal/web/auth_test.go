package web

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthHandlerRequiresLoginAndCreatesSession(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	handler, err := NewAuthHandler(log.New(io.Discard, "", 0), next, AuthConfig{
		Username:   "admin",
		Password:   "secret",
		SessionTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("NewAuthHandler() error = %v", err)
	}

	unauthReq := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	unauthReq.RemoteAddr = "203.0.113.10:12345"
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated status = %d, want %d", unauthRec.Code, http.StatusSeeOther)
	}
	if location := unauthRec.Header().Get("Location"); location != "/login" {
		t.Fatalf("redirect location = %q, want /login", location)
	}

	form := url.Values{"username": {"admin"}, "password": {"secret"}}
	loginReq := httptest.NewRequest(http.MethodPost, "http://example.test/login", strings.NewReader(form.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginReq.RemoteAddr = "203.0.113.10:12345"
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusSeeOther, loginRec.Body.String())
	}

	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	authReq := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	authReq.RemoteAddr = "203.0.113.10:12345"
	authReq.AddCookie(cookies[0])
	authRec := httptest.NewRecorder()
	handler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d, want %d", authRec.Code, http.StatusNoContent)
	}
}

func TestAuthHandlerRejectsBadPassword(t *testing.T) {
	handler, err := NewAuthHandler(log.New(io.Discard, "", 0), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), AuthConfig{
		Username: "admin",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("NewAuthHandler() error = %v", err)
	}

	form := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "http://example.test/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.11:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad-password status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestValidateSameOriginRequestAcceptsOpaqueBrowserOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.test/login", nil)
	req.Host = "example.test"
	req.Header.Set("Origin", "null")

	if err := validateSameOriginRequest(req); err != nil {
		t.Fatalf("validateSameOriginRequest() error = %v, want nil", err)
	}

	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if err := validateSameOriginRequest(req); err == nil {
		t.Fatal("validateSameOriginRequest() accepted explicit cross-site opaque origin")
	}
}
