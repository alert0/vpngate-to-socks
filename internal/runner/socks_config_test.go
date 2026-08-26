package runner

import (
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestSOCKSConfigUpdatePersistsAndRebinds(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "socks.json")
	t.Setenv("SOCKS_CONFIG_FILE", configPath)
	t.Setenv("SOCKS_USERNAME", "")
	t.Setenv("SOCKS_PASSWORD", "")
	t.Setenv("SOCKS_LISTEN_ADDR", "")

	firstAddr := reserveTCPAddr(t)
	server, err := newSOCKSServer(log.New(io.Discard, "", 0), firstAddr, nil)
	if err != nil {
		t.Fatalf("newSOCKSServer() error = %v", err)
	}
	defer server.Close()

	if config := server.Config(); config.PasswordConfigured {
		t.Fatal("initial PasswordConfigured = true, want false")
	}

	secondAddr := reserveTCPAddr(t)
	config, err := server.UpdateConfig(SOCKSConfigUpdate{
		ListenAddr: secondAddr,
		Username:   "proxy-user",
		Password:   "secret-password",
	})
	if err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if config.ListenAddr != secondAddr {
		t.Fatalf("ListenAddr = %q, want %q", config.ListenAddr, secondAddr)
	}
	if config.Username != "proxy-user" || !config.PasswordConfigured {
		t.Fatalf("config = %+v, want configured proxy-user", config)
	}
	if got := server.ListenAddr(); got != secondAddr {
		t.Fatalf("server.ListenAddr() = %q, want %q", got, secondAddr)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("os.Stat(configPath) error = %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config permissions = %o, want no group/other permissions", info.Mode().Perm())
	}

	if err := server.Close(); err != nil {
		t.Fatalf("server.Close() error = %v", err)
	}

	reloaded, err := newSOCKSServer(log.New(io.Discard, "", 0), firstAddr, nil)
	if err != nil {
		t.Fatalf("reload newSOCKSServer() error = %v", err)
	}
	defer reloaded.Close()

	reloadedConfig := reloaded.Config()
	if reloadedConfig.ListenAddr != secondAddr || reloadedConfig.Username != "proxy-user" || !reloadedConfig.PasswordConfigured {
		t.Fatalf("reloaded config = %+v", reloadedConfig)
	}
}

func TestSOCKSConfigBlankPasswordKeepsCurrentPassword(t *testing.T) {
	t.Setenv("SOCKS_CONFIG_FILE", filepath.Join(t.TempDir(), "socks.json"))
	t.Setenv("SOCKS_USERNAME", "")
	t.Setenv("SOCKS_PASSWORD", "")
	t.Setenv("SOCKS_LISTEN_ADDR", "")

	addr := reserveTCPAddr(t)
	server, err := newSOCKSServer(log.New(io.Discard, "", 0), addr, nil)
	if err != nil {
		t.Fatalf("newSOCKSServer() error = %v", err)
	}
	defer server.Close()

	if _, err := server.UpdateConfig(SOCKSConfigUpdate{ListenAddr: addr, Username: "first", Password: "secret"}); err != nil {
		t.Fatalf("first UpdateConfig() error = %v", err)
	}
	config, err := server.UpdateConfig(SOCKSConfigUpdate{ListenAddr: addr, Username: "second", Password: ""})
	if err != nil {
		t.Fatalf("second UpdateConfig() error = %v", err)
	}
	if config.Username != "second" || !config.PasswordConfigured {
		t.Fatalf("config = %+v", config)
	}
	username, password, enabled := server.credentials()
	if !enabled || username != "second" || password != "secret" {
		t.Fatalf("credentials = (%q, %q, %v), want second/secret/true", username, password, enabled)
	}
}

func TestSOCKSConfigRejectsInvalidListenAddressWithoutChangingCurrent(t *testing.T) {
	t.Setenv("SOCKS_CONFIG_FILE", filepath.Join(t.TempDir(), "socks.json"))
	t.Setenv("SOCKS_USERNAME", "")
	t.Setenv("SOCKS_PASSWORD", "")
	t.Setenv("SOCKS_LISTEN_ADDR", "")

	addr := reserveTCPAddr(t)
	server, err := newSOCKSServer(log.New(io.Discard, "", 0), addr, nil)
	if err != nil {
		t.Fatalf("newSOCKSServer() error = %v", err)
	}
	defer server.Close()

	if _, err := server.UpdateConfig(SOCKSConfigUpdate{ListenAddr: addr, Username: "proxy", Password: "secret"}); err != nil {
		t.Fatalf("initial UpdateConfig() error = %v", err)
	}
	if _, err := server.UpdateConfig(SOCKSConfigUpdate{ListenAddr: "0.0.0.0:70000", Username: "proxy"}); err == nil {
		t.Fatal("invalid UpdateConfig() unexpectedly succeeded")
	}
	if got := server.ListenAddr(); got != addr {
		t.Fatalf("ListenAddr() after failed update = %q, want %q", got, addr)
	}
}

func reserveTCPAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}
	return addr
}
