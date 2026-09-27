package ftp

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/go-filesystem/v2"
)

func TestOpenRejectsUnisolatedSubroot(t *testing.T) {
	for _, root := range []string{"/tenant", "//tenant//", "/tenant/."} {
		t.Run(root, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := Open(ctx, Config{
				Address: "invalid-address", Username: "user", Password: "password",
				Root: root, TLSMode: TLSPlaintext, AllowPlaintext: true,
				Timeout: time.Millisecond,
			})
			if err == nil || !strings.Contains(err.Error(), "subroot") {
				t.Fatalf("Open(%q) = %v; want subroot rejection before dialing", root, err)
			}
		})
	}
}

func TestOpenPermitsServerRootAndExplicitUnsafeSubroot(t *testing.T) {
	for _, configuration := range []Config{
		{Root: ""}, {Root: "/"}, {Root: "//"}, {Root: "/."},
		{Root: "/tenant", AllowUnsafeSubroot: true},
	} {
		t.Run(configuration.Root, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			accepted := make(chan bool, 1)
			go func() {
				connection, acceptErr := listener.Accept()
				if acceptErr == nil {
					_ = connection.Close()
				}
				accepted <- acceptErr == nil
			}()
			t.Cleanup(func() { _ = listener.Close() })
			configuration.Address, configuration.Username, configuration.Password = listener.Addr().String(), "user", "password"
			configuration.TLSMode, configuration.AllowPlaintext, configuration.Timeout = TLSPlaintext, true, time.Second
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err = Open(ctx, configuration)
			if err == nil || strings.Contains(err.Error(), "subroot") {
				t.Fatalf("Open(%q) = %v; want connection attempt", configuration.Root, err)
			}
			_ = listener.Close()
			if !<-accepted {
				t.Fatal("accepted configuration did not dial")
			}
		})
	}
}

// This characterizes why a remote subroot must be an explicit unsafe opt-in:
// server path resolution can change after every client-side link check.
func TestRemoteLinkPreflightIsNotConfinementBoundary(t *testing.T) {
	state := newFakeState()
	state.objects["/tenant/directory/file"] = []byte("inside")
	state.objects["/outside/file"] = []byte("outside")
	session := &linkSwapSession{fakeSession: &fakeSession{state: state}}
	adapter, err := newAdapter(context.Background(), func(context.Context) (remoteSession, error) {
		return session, nil
	}, "/tenant", 100, Profile{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	stream, err := adapter.Open(context.Background(), filesystem.MustParsePath("directory/file"))
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil || string(content) != "outside" {
		t.Fatalf("server-side link swap: content %q, read %v, close %v", content, readErr, closeErr)
	}
}

type linkSwapSession struct {
	*fakeSession
	swapped bool
}

func (s *linkSwapSession) Stat(name string) (remoteEntry, error) {
	entry, err := s.fakeSession.Stat(name)
	if name == "/tenant/directory/file" && err == nil {
		s.swapped = true
	}
	return entry, err
}

func (s *linkSwapSession) Retrieve(name string, destination io.Writer) error {
	if s.swapped && name == "/tenant/directory/file" {
		name = "/outside/file"
	}
	return s.fakeSession.Retrieve(name, destination)
}
