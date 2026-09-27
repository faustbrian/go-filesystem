package sftp

import (
	"context"
	"io"
	"io/fs"
	"net"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	"golang.org/x/crypto/ssh"
)

func TestOpenRejectsUnisolatedSubroot(t *testing.T) {
	for _, root := range []string{"/tenant", "//tenant//", "/tenant/."} {
		t.Run(root, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := Open(ctx, Config{
				Address: "invalid-address", User: "user", Auth: []ssh.AuthMethod{ssh.Password("password")},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(), Root: root, Timeout: time.Millisecond,
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
			configuration.Address, configuration.User = listener.Addr().String(), "user"
			configuration.Auth, configuration.HostKeyCallback = []ssh.AuthMethod{ssh.Password("password")}, ssh.InsecureIgnoreHostKey()
			configuration.Timeout = time.Second
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
	}, "/tenant", 100)
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

func (s *linkSwapSession) Lstat(name string) (fs.FileInfo, error) {
	info, err := s.fakeSession.Lstat(name)
	if name == "/tenant/directory/file" && err == nil {
		s.swapped = true
	}
	return info, err
}

func (s *linkSwapSession) Open(name string) (remoteFile, error) {
	if s.swapped && name == "/tenant/directory/file" {
		name = "/outside/file"
	}
	return s.fakeSession.Open(name)
}
