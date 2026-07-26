package sftp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/faustbrian/golib/pkg/filesystem/fstest"
	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestRealSFTPServerConformance(t *testing.T) {
	server := newLoopbackServer(t)
	fstest.TestFilesystem(t, func(t *testing.T) fstest.Filesystem {
		t.Helper()
		root := filepath.Join(t.TempDir(), "storage")
		adapter, err := New(context.Background(), Config{
			Address:         server.address,
			User:            "test",
			Auth:            []ssh.AuthMethod{ssh.Password("secret")},
			HostKeyCallback: ssh.FixedHostKey(server.hostKey),
			Root:            root,
			MaxListEntries:  100,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := adapter.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		})
		return adapter
	})
}

func TestRealSessionStandardRename(t *testing.T) {
	server := newLoopbackServer(t)
	root := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter, err := New(context.Background(), Config{
		Address:         server.address,
		User:            "test",
		Auth:            []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.FixedHostKey(server.hostKey),
		Root:            root,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	session := adapter.session.(*realSession)
	if err := session.Rename(filepath.Join(root, "source"), filepath.Join(root, "destination")); err != nil {
		t.Fatal(err)
	}
}

func TestNewReportsSSHHandshakeAndSubsystemFailures(t *testing.T) {
	handshakeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, acceptErr := handshakeListener.Accept()
		if acceptErr == nil {
			_ = connection.Close()
		}
	}()
	configuration := Config{
		Address:         handshakeListener.Addr().String(),
		User:            "test",
		Auth:            []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	if _, err := New(context.Background(), configuration); err == nil {
		t.Fatal("New() handshake error = nil")
	}
	_ = handshakeListener.Close()
	<-done

	server := newRejectingSubsystemServer(t)
	configuration.Address = server.address
	configuration.HostKeyCallback = ssh.FixedHostKey(server.hostKey)
	if _, err := New(context.Background(), configuration); err == nil {
		t.Fatal("New() subsystem error = nil")
	}
}

type loopbackServer struct {
	address string
	hostKey ssh.PublicKey
}

func newLoopbackServer(t *testing.T) loopbackServer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	configuration := &ssh.ServerConfig{
		PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if metadata.User() != "test" || string(password) != "secret" {
				return nil, errors.New("authentication failed")
			}
			return nil, nil
		},
	}
	configuration.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			wait.Add(1)
			go func() {
				defer wait.Done()
				serveSSHConnection(connection, configuration)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		wait.Wait()
	})
	return loopbackServer{address: listener.Addr().String(), hostKey: ssh.PublicKey(signer.PublicKey())}
}

func newRejectingSubsystemServer(t *testing.T) loopbackServer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	configuration := &ssh.ServerConfig{
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	configuration.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		serverConnection, channels, requests, handshakeErr := ssh.NewServerConn(connection, configuration)
		if handshakeErr != nil {
			_ = connection.Close()
			return
		}
		defer func() { _ = serverConnection.Close() }()
		go ssh.DiscardRequests(requests)
		for channelRequest := range channels {
			channel, channelRequests, acceptErr := channelRequest.Accept()
			if acceptErr != nil {
				continue
			}
			for request := range channelRequests {
				_ = request.Reply(false, nil)
				_ = channel.Close()
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		wait.Wait()
	})
	return loopbackServer{address: listener.Addr().String(), hostKey: signer.PublicKey()}
}

func serveSSHConnection(connection net.Conn, configuration *ssh.ServerConfig) {
	serverConnection, channels, requests, err := ssh.NewServerConn(connection, configuration)
	if err != nil {
		_ = connection.Close()
		return
	}
	defer func() { _ = serverConnection.Close() }()
	go ssh.DiscardRequests(requests)
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			_ = channelRequest.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		channel, requests, err := channelRequest.Accept()
		if err != nil {
			continue
		}
		for request := range requests {
			var subsystem struct{ Name string }
			valid := request.Type == "subsystem" && ssh.Unmarshal(request.Payload, &subsystem) == nil && subsystem.Name == "sftp"
			_ = request.Reply(valid, nil)
			if !valid {
				continue
			}
			server, err := pkgsftp.NewServer(channel)
			if err == nil {
				if serveErr := server.Serve(); serveErr != nil && !errors.Is(serveErr, io.EOF) {
					_ = server.Close()
					_ = channel.Close()
					return
				}
				_ = server.Close()
			}
			_ = channel.Close()
			return
		}
	}
}
