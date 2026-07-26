package ftp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	filesystem "github.com/faustbrian/golib/pkg/filesystem"
	protocolserver "github.com/gonzalop/ftp/server"
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
				Timeout:        5 * time.Second,
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
	driver, err := protocolserver.NewFSDriver(root,
		protocolserver.WithAuthenticator(func(user, password, _ string, _ net.IP) (string, bool, error) {
			if user != "user" || password != "password" {
				return "", false, os.ErrPermission
			}
			return root, false, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	options := []protocolserver.Option{protocolserver.WithDriver(driver)}
	if mode != TLSPlaintext {
		options = append(options, protocolserver.WithTLS(tlsConfiguration))
	}
	server, err := protocolserver.NewServer(listener.Addr().String(), options...)
	if err != nil {
		t.Fatal(err)
	}
	if mode == TLSImplicit {
		listener = tls.NewListener(listener, tlsConfiguration)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("server Shutdown() error = %v", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, protocolserver.ErrServerClosed) {
			t.Errorf("server Serve() error = %v", err)
		}
	})
	return listener.Addr().String()
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
