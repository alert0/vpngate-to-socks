package main

import "testing"

func TestSOCKSListenAddrDefaultPort(t *testing.T) {
	t.Setenv("SOCKS_LISTEN_ADDR", "")

	if got, want := socksListenAddr(), "0.0.0.0:5888"; got != want {
		t.Fatalf("socksListenAddr() = %q, want %q", got, want)
	}
}
