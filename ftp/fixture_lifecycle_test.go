package ftp

import (
	"bufio"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestLoopbackFTPListenerClosesAcceptedConnections(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := newLoopbackFTPListener(base)
	t.Cleanup(func() { _ = listener.Close() })
	var clients []net.Conn
	for range 2 {
		accepted := make(chan net.Conn, 1)
		go func() {
			connection, _ := listener.Accept()
			accepted <- connection
		}()
		client, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		server := <-accepted
		if server == nil {
			t.Fatal("Accept failed")
		}
		t.Cleanup(func() { _ = server.Close() })
		clients = append(clients, client)
	}
	// No protocol driver or authentication callback has registered these sockets.
	// Listener shutdown must nevertheless close every accepted control connection.
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	listener.closeConnections()
	for _, client := range clients {
		if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		_, err := client.Read(make([]byte, 1))
		if !errors.Is(err, io.EOF) {
			t.Fatalf("read after fixture shutdown = %v; want EOF", err)
		}
	}
}

func TestLoopbackFTPListenerDoesNotEscapeInFlightAccept(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	paused := &pausedFTPListener{Listener: base, accepted: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-paused.release:
		default:
			close(paused.release)
		}
	})
	listener := newLoopbackFTPListener(paused)
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if connection != nil {
			defer func() { _ = connection.Close() }()
		}
		accepted <- err
	}()
	client, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	select {
	case <-paused.accepted:
	case <-time.After(time.Second):
		t.Fatal("connection was not accepted")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	listener.closeConnections()
	close(paused.release)
	if err := <-accepted; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("in-flight Accept after shutdown = %v; want net.ErrClosed", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("connection accepted during shutdown = %v; want EOF", err)
	}
}

func TestLoopbackFTPServerCleanupClosesIdleSession(t *testing.T) {
	var client net.Conn
	t.Cleanup(func() {
		if client != nil {
			_ = client.Close()
		}
	})
	if !t.Run("fixture", func(t *testing.T) {
		address := startModeServer(t, t.TempDir(), TLSPlaintext, nil)
		var err error
		client, err = net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := bufio.NewReader(client).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		// Leave the authenticated protocol's command-loop owner idle before USER;
		// fixture cleanup must close its socket and observe ClientDisconnected.
	}) {
		return
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("idle session after fixture cleanup = %v; want EOF", err)
	}
}

type pausedFTPListener struct {
	net.Listener
	accepted, release chan struct{}
}

func (l *pausedFTPListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err == nil {
		close(l.accepted)
		<-l.release
	}
	return connection, err
}
