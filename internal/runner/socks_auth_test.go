package runner

import (
	"io"
	"net"
	"testing"
)

func TestAuthenticateSOCKSUserPassSuccess(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- authenticateSOCKSUserPass(server, "proxy", "secret")
	}()

	request := []byte{socksUserPassVersion, 5}
	request = append(request, []byte("proxy")...)
	request = append(request, 6)
	request = append(request, []byte("secret")...)
	if _, err := client.Write(request); err != nil {
		t.Fatalf("client.Write() error = %v", err)
	}

	response := make([]byte, 2)
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatalf("io.ReadFull() error = %v", err)
	}
	if response[0] != socksUserPassVersion || response[1] != socksUserPassSuccess {
		t.Fatalf("auth response = %v, want [%d %d]", response, socksUserPassVersion, socksUserPassSuccess)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("authenticateSOCKSUserPass() error = %v", err)
	}
}

func TestAuthenticateSOCKSUserPassFailure(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- authenticateSOCKSUserPass(server, "proxy", "secret")
	}()

	request := []byte{socksUserPassVersion, 5}
	request = append(request, []byte("proxy")...)
	request = append(request, 5)
	request = append(request, []byte("wrong")...)
	if _, err := client.Write(request); err != nil {
		t.Fatalf("client.Write() error = %v", err)
	}

	response := make([]byte, 2)
	if _, err := io.ReadFull(client, response); err != nil {
		t.Fatalf("io.ReadFull() error = %v", err)
	}
	if response[1] != socksUserPassFailure {
		t.Fatalf("auth status = %d, want %d", response[1], socksUserPassFailure)
	}
	if err := <-errCh; err == nil {
		t.Fatal("authenticateSOCKSUserPass() unexpectedly succeeded")
	}
}
