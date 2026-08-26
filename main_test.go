package main

import "testing"

func TestWebListenAddrDefaultPort(t *testing.T) {
	t.Setenv("WEB_LISTEN_ADDR", "")
	t.Setenv("PORT", "")

	if got, want := webListenAddr(), "0.0.0.0:5777"; got != want {
		t.Fatalf("webListenAddr() = %q, want %q", got, want)
	}
}
