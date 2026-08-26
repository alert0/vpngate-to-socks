package web

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"vpngate/internal/runner"
)

type fakeSOCKSSettingsControl struct {
	config     runner.SOCKSConfig
	lastUpdate runner.SOCKSConfigUpdate
	err        error
}

func (f *fakeSOCKSSettingsControl) Enabled() bool { return true }

func (f *fakeSOCKSSettingsControl) SOCKSConfig(context.Context) (runner.SOCKSConfig, error) {
	return f.config, f.err
}

func (f *fakeSOCKSSettingsControl) UpdateSOCKSConfig(_ context.Context, update runner.SOCKSConfigUpdate) (runner.SOCKSConfig, error) {
	f.lastUpdate = update
	if f.err != nil {
		return f.config, f.err
	}
	f.config.ListenAddr = update.ListenAddr
	f.config.Username = update.Username
	if update.Password != "" {
		f.config.PasswordConfigured = true
	}
	return f.config, nil
}

func TestSOCKSSettingsPageDoesNotExposePassword(t *testing.T) {
	control := &fakeSOCKSSettingsControl{config: runner.SOCKSConfig{
		ListenAddr:         "0.0.0.0:1080",
		Username:           "proxy-user",
		PasswordConfigured: true,
	}}
	handler := NewSOCKSSettingsHandler(log.New(io.Discard, "", 0), control)

	req := httptest.NewRequest(http.MethodGet, "/settings/socks", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "proxy-user") || !strings.Contains(body, "当前密码已设置") {
		t.Fatalf("page missing current config: %s", body)
	}
	if strings.Contains(body, "secret-password") {
		t.Fatal("page unexpectedly exposed a password")
	}
}

func TestSOCKSSettingsUpdateKeepsBlankPassword(t *testing.T) {
	control := &fakeSOCKSSettingsControl{config: runner.SOCKSConfig{
		ListenAddr:         "0.0.0.0:1080",
		Username:           "old-user",
		PasswordConfigured: true,
	}}
	handler := NewSOCKSSettingsHandler(log.New(io.Discard, "", 0), control)

	form := url.Values{
		"listen_addr": {"0.0.0.0:2080"},
		"username":    {"new-user"},
		"password":    {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/settings/socks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusSeeOther, recorder.Body.String())
	}
	if control.lastUpdate.ListenAddr != "0.0.0.0:2080" || control.lastUpdate.Username != "new-user" {
		t.Fatalf("lastUpdate = %+v", control.lastUpdate)
	}
	if control.lastUpdate.Password != "" {
		t.Fatalf("Password = %q, want blank keep-password marker", control.lastUpdate.Password)
	}
	if location := recorder.Header().Get("Location"); !strings.HasPrefix(location, "/settings/socks?notice=") {
		t.Fatalf("Location = %q", location)
	}
}
