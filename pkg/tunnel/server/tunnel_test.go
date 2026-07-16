package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	tunnelbroker "github.com/tardigradeproj/heir/pkg/tunnel/server/broker"
	"github.com/tardigradeproj/outbound"
)

const (
	echoUpstreamID uint8         = 1
	serveKeepAlive time.Duration = 30 * time.Second
)

// testPKI holds a test CA and a server cert/key pair written to temp files.
type testPKI struct {
	caCertFile     string
	serverCertFile string
	serverKeyFile  string
	caCert         *x509.Certificate
	caKey          *ecdsa.PrivateKey
	caCertPool     *x509.CertPool
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
	}
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caCertDER)
	require.NoError(t, err)

	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	srvTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-server"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	srvCertDER, err := x509.CreateCertificate(rand.Reader, srvTemplate, caCert, &srvKey.PublicKey, caKey)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	return &testPKI{
		caCertFile:     pemFile(t, "CERTIFICATE", caCertDER),
		serverCertFile: pemFile(t, "CERTIFICATE", srvCertDER),
		serverKeyFile:  ecKeyFile(t, srvKey),
		caCert:         caCert,
		caKey:          caKey,
		caCertPool:     pool,
	}
}

// clientTLSConfig returns a TLS config with a fresh client cert signed by the CA using the given CN.
func (pki *testPKI) clientTLSConfig(t *testing.T, cn string) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: cn},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, pki.caCert, &key.PublicKey, pki.caKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	tlsCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	require.NoError(t, err)
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		RootCAs:      pki.caCertPool,
		ServerName:   "127.0.0.1",
	}
}

func pemFile(t *testing.T, blockType string, der []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.pem")
	require.NoError(t, err)
	require.NoError(t, pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}))
	require.NoError(t, f.Close())
	return f.Name()
}

func ecKeyFile(t *testing.T, key *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return pemFile(t, "EC PRIVATE KEY", der)
}

// freeAddr returns a TCP address with an available port on loopback.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// startEchoServer starts a TCP echo server and returns its address.
func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return ln.Addr().String()
}

func TestServe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		assertion func(t *testing.T, b *tunnelbroker.Broker, registry *outbound.Registry, connectFn func(cn string) (*tls.Conn, error))
	}{
		{
			name: "client tunnel dials registered upstream and data round-trips",
			assertion: func(t *testing.T, b *tunnelbroker.Broker, registry *outbound.Registry, connectFn func(cn string) (*tls.Conn, error)) {
				const nodeName = "worker-1"

				registry.Register(outbound.Upstream{
					Id:   echoUpstreamID,
					Name: "echo",
					Dial: outbound.TCPUpstream(startEchoServer(t)),
				})

				tlsConn, err := connectFn("system:node:" + nodeName)
				require.NoError(t, err)
				t.Cleanup(func() { _ = tlsConn.Close() })

				clientSession, err := outbound.Client(tlsConn, nil)
				require.NoError(t, err)
				clientTunnel := outbound.NewTunnel(clientSession, outbound.NewRegistry())

				require.Eventually(t, func() bool {
					return b.Pick(nodeName) != nil
				}, 2*time.Second, 10*time.Millisecond)

				stream, err := clientTunnel.Dial(context.Background(), echoUpstreamID)
				require.NoError(t, err)
				t.Cleanup(func() { _ = stream.Close() })

				_, err = stream.Write([]byte("hello"))
				require.NoError(t, err)

				buf := make([]byte, 5)
				_, err = io.ReadFull(stream, buf)
				require.NoError(t, err)
				require.Equal(t, "hello", string(buf))
			},
		},
		{
			name: "connection with non-conforming CN is not registered in broker",
			assertion: func(t *testing.T, b *tunnelbroker.Broker, registry *outbound.Registry, connectFn func(cn string) (*tls.Conn, error)) {
				tlsConn, err := connectFn("not-a-node")
				require.NoError(t, err)
				t.Cleanup(func() { _ = tlsConn.Close() })

				// handle rejects CN that lacks the system:node: prefix; no tunnel is registered.
				require.Never(t, func() bool {
					return b.Pick("not-a-node") != nil
				}, 300*time.Millisecond, 10*time.Millisecond)
			},
		},
		{
			name: "connection with empty node name suffix is not registered in broker",
			assertion: func(t *testing.T, b *tunnelbroker.Broker, registry *outbound.Registry, connectFn func(cn string) (*tls.Conn, error)) {
				tlsConn, err := connectFn("system:node:")
				require.NoError(t, err)
				t.Cleanup(func() { _ = tlsConn.Close() })

				// handle rejects CN whose node name part is empty; broker must not gain an empty-keyed tunnel.
				require.Never(t, func() bool {
					return b.Pick("") != nil
				}, 300*time.Millisecond, 10*time.Millisecond)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pki := newTestPKI(t)
			registry := outbound.NewRegistry()
			b := tunnelbroker.New(registry, serveKeepAlive)
			addr := freeAddr(t)
			ts := NewTunnelServer(pki.serverCertFile, pki.serverKeyFile, pki.caCertFile, addr, b)

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			go ts.Serve(ctx) //nolint:errcheck

			// Wait until the listener is ready to accept TCP connections.
			require.Eventually(t, func() bool {
				c, err := net.Dial("tcp", addr)
				if err != nil {
					return false
				}
				_ = c.Close()
				return true
			}, 2*time.Second, 10*time.Millisecond)

			connectFn := func(cn string) (*tls.Conn, error) {
				return tls.Dial("tcp", addr, pki.clientTLSConfig(t, cn))
			}

			tc.assertion(t, b, registry, connectFn)
		})
	}
}
