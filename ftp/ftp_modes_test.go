package ftp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	filesystem "github.com/faustbrian/go-filesystem/v2"
	protocolserver "github.com/fclairamb/ftpserverlib"
	"github.com/spf13/afero"
)

func TestConcreteTransportModeMatrix(t *testing.T) {
	serverTLS, _ := testTLSConfigurations(t)
	for _, dataMode := range []DataMode{Passive, Active} {
		t.Run("plaintext/"+dataModeName(dataMode), func(t *testing.T) {
			root := t.TempDir()
			address := startModeServer(t, root, TLSPlaintext, serverTLS)
			configuration := Config{
				Address:        address,
				Username:       "user",
				Password:       "password",
				TLSMode:        TLSPlaintext,
				AllowPlaintext: true,
				DataMode:       dataMode,
				Timeout:        30 * time.Second,
			}
			adapter, err := New(context.Background(), configuration)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = adapter.Close() })
			path := filesystem.MustParsePath("mode/雪 file.txt")
			if _, err := adapter.Write(context.Background(), path, strings.NewReader("mode-content"), filesystem.WriteOptions{}); err != nil {
				t.Fatal(err)
			}
			stream, err := adapter.Open(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			content, readErr := io.ReadAll(stream)
			closeErr := stream.Close()
			if readErr != nil || closeErr != nil || string(content) != "mode-content" {
				t.Fatalf("Open() = %q, read %v, close %v", content, readErr, closeErr)
			}
			profile := adapter.Profile()
			if profile.TLSMode != TLSPlaintext || profile.DataMode != dataMode {
				t.Fatalf("Profile() = %+v", profile)
			}
		})
	}
}

func TestFTPSModesAreRejectedBeforeDial(t *testing.T) {
	t.Parallel()

	_, clientTLS := testTLSConfigurations(t)
	for _, mode := range []TLSMode{TLSExplicit, TLSImplicit} {
		for _, dataMode := range []DataMode{Passive, Active} {
			_, err := New(context.Background(), Config{
				Address:   "127.0.0.1:1",
				Username:  "user",
				Password:  "password",
				TLSMode:   mode,
				TLSConfig: clientTLS,
				DataMode:  dataMode,
			})
			if err == nil || !strings.Contains(err.Error(), "TLS data transfers are not supported") {
				t.Fatalf("New(%s/%s) error = %v", tlsModeName(mode), dataModeName(dataMode), err)
			}
		}
	}
}

func startModeServer(t *testing.T, root string, mode TLSMode, tlsConfiguration *tls.Config) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if mode == TLSImplicit {
		listener = tls.NewListener(listener, tlsConfiguration)
	}
	driver := &loopbackFTPDriver{root: root, listener: listener, mode: mode, tls: tlsConfiguration}
	// Use an independent server: the client's own v1.6.1 server double-cleans
	// completed transfers and can cancel the next transfer after sending 226.
	server := protocolserver.NewFtpServer(driver)
	if err := server.Listen(); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve() }()
	t.Cleanup(func() {
		driver.closeClients()
		if err := server.Stop(); err != nil {
			t.Errorf("server Stop() error = %v", err)
		}
		if err := <-serveErrors; err != nil {
			t.Errorf("server Serve() error = %v", err)
		}
	})
	return listener.Addr().String()
}

// The fixture owns accepted sessions as well as the control/passive listeners;
// ftpserverlib.Stop alone closes only listeners, not connected clients.
type loopbackFTPDriver struct {
	root     string
	listener net.Listener
	mode     TLSMode
	tls      *tls.Config
	mu       sync.Mutex
	clients  []protocolserver.ClientContext
	closed   bool
}

func (d *loopbackFTPDriver) GetSettings() (*protocolserver.Settings, error) {
	requirement := protocolserver.ClearOrEncrypted
	if d.mode == TLSImplicit {
		requirement = protocolserver.ImplicitEncryption
	}
	if d.mode == TLSExplicit {
		requirement = protocolserver.MandatoryEncryption
	}
	return &protocolserver.Settings{
		Listener: d.listener, PublicHost: "127.0.0.1", ActiveTransferPortNon20: true,
		IdleTimeout: 30, ConnectionTimeout: 30, TLSRequired: requirement,
	}, nil
}

func (d *loopbackFTPDriver) ClientConnected(client protocolserver.ClientContext) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return "", net.ErrClosed
	}
	d.clients = append(d.clients, client)
	return "loopback FTP fixture", nil
}

func (*loopbackFTPDriver) ClientDisconnected(protocolserver.ClientContext) {}

func (d *loopbackFTPDriver) AuthUser(_ protocolserver.ClientContext, user, password string) (protocolserver.ClientDriver, error) {
	if user != "user" || password != "password" {
		return nil, os.ErrPermission
	}
	return afero.NewBasePathFs(afero.NewOsFs(), d.root), nil
}

func (d *loopbackFTPDriver) GetTLSConfig() (*tls.Config, error) { return d.tls, nil }

func (d *loopbackFTPDriver) closeClients() {
	d.mu.Lock()
	d.closed = true
	clients := append([]protocolserver.ClientContext(nil), d.clients...)
	d.mu.Unlock()
	for _, client := range clients {
		_ = client.Close()
	}
}

func testTLSConfigurations(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	server := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{certificateDER}, PrivateKey: privateKey}},
		MinVersion:   tls.VersionTLS12,
	}
	client := &tls.Config{
		RootCAs:    pool,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	}
	return server, client
}

func tlsModeName(mode TLSMode) string {
	switch mode {
	case TLSPlaintext:
		return "plaintext"
	case TLSExplicit:
		return "explicit"
	case TLSImplicit:
		return "implicit"
	default:
		return "unknown"
	}
}

func dataModeName(mode DataMode) string {
	if mode == Active {
		return "active"
	}
	return "passive"
}
