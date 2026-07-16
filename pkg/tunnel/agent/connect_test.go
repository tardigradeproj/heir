package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tardigradeproj/heir/pkg/tunnel/shrd"
	"github.com/tardigradeproj/outbound"
)

// testPKI holds in-memory TLS assets and on-disk paths used to configure the
// agent under test and the fake outbound server.
type testPKI struct {
	caCert         *x509.Certificate
	serverTLSCert  tls.Certificate
	caFile         string
	clientCertFile string
	clientKeyFile  string
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caCertDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
	}, &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
	}, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	caCert, err := x509.ParseCertificate(caCertDER)
	require.NoError(t, err)

	signCert := func(tmpl *x509.Certificate) (tls.Certificate, []byte, []byte) {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		require.NoError(t, err)
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
		keyDER, err := x509.MarshalECPrivateKey(key)
		require.NoError(t, err)
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
		require.NoError(t, err)
		return tlsCert, certPEM, keyPEM
	}

	serverTLSCert, _, _ := signCert(&x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-server"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	})

	_, clientCertPEM, clientKeyPEM := signCert(&x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "system:node:test"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	})

	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	return &testPKI{
		caCert:         caCert,
		serverTLSCert:  serverTLSCert,
		caFile:         writePEMFile(t, caCertPEM),
		clientCertFile: writePEMFile(t, clientCertPEM),
		clientKeyFile:  writePEMFile(t, clientKeyPEM),
	}
}

func writePEMFile(t *testing.T, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.pem")
	require.NoError(t, err)
	_, err = f.Write(data)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

// newMTLSServer starts a mTLS listener backed by pki and dispatches each
// accepted connection to handler in its own goroutine. The listener is closed
// via t.Cleanup.
func newMTLSServer(t *testing.T, pki *testPKI, handler func(conn net.Conn)) string {
	t.Helper()
	caPool := x509.NewCertPool()
	caPool.AddCert(pki.caCert)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{pki.serverTLSCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   tls.VersionTLS12,
	})
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handler(conn)
	}()
	return ln.Addr().String()
}

// outboundHandler returns a server-side connection handler that speaks the
// outbound protocol and serves the given identity on upstream 0.
func outboundHandler(identity *shrd.PlaneTunnelIdentity) func(net.Conn) {
	return func(conn net.Conn) {
		defer conn.Close()
		session, err := outbound.Server(conn, nil)
		if err != nil {
			return
		}
		registry := outbound.NewRegistry()
		registry.Register(outbound.Upstream{
			Id:   shrd.IdentityUpstreamID,
			Name: "identity",
			Dial: func(ctx context.Context) (net.Conn, error) {
				local, remote := net.Pipe()
				payload, _ := json.Marshal(identity)
				go func() { _, _ = local.Write(payload); _ = local.Close() }()
				return remote, nil
			},
		})
		tunnel := outbound.NewTunnel(session, registry)
		_ = tunnel.Serve(context.Background())
	}
}

func newTestAgent(t *testing.T, pki *testPKI, addr string) *Agent {
	t.Helper()
	a, err := New(pki.clientCertFile, pki.clientKeyFile, pki.caFile, addr, "127.0.0.1:10250", time.Second)
	require.NoError(t, err)
	return a
}

// ---- test ----

type result struct {
	tunnel   *outbound.Tunnel
	identity *shrd.PlaneTunnelIdentity
	err      error
}

func assertion(t *testing.T, got result, wantErr bool, wantIdentity *shrd.PlaneTunnelIdentity) {
	t.Helper()
	if wantErr {
		require.Error(t, got.err)
		assert.Nil(t, got.tunnel)
		assert.Nil(t, got.identity)
		return
	}
	require.NoError(t, got.err)
	require.NotNil(t, got.tunnel)
	require.NotNil(t, got.identity)
	if wantIdentity != nil {
		assert.Equal(t, wantIdentity.Id, got.identity.Id)
		assert.Equal(t, wantIdentity.NumberOfInstances, got.identity.NumberOfInstances)
	}
}

func TestConnect(t *testing.T) {
	pki := newTestPKI(t)
	wantIdentity := &shrd.PlaneTunnelIdentity{Id: "abc-123", NumberOfInstances: 3}

	cases := []struct {
		name         string
		setupServer  func(t *testing.T) string
		wantErr      bool
		wantIdentity *shrd.PlaneTunnelIdentity
	}{
		{
			name: "valid connection returns tunnel and identity",
			setupServer: func(t *testing.T) string {
				return newMTLSServer(t, pki, outboundHandler(wantIdentity))
			},
			wantIdentity: wantIdentity,
		},
		{
			name: "no server at address returns dial error",
			setupServer: func(t *testing.T) string {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				addr := ln.Addr().String()
				require.NoError(t, ln.Close())
				return addr
			},
			wantErr: true,
		},
		{
			name: "server certificate from unknown CA returns TLS error",
			setupServer: func(t *testing.T) string {
				otherPKI := newTestPKI(t)
				ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
					Certificates: []tls.Certificate{otherPKI.serverTLSCert},
					MinVersion:   tls.VersionTLS12,
				})
				require.NoError(t, err)
				t.Cleanup(func() { ln.Close() })
				go func() {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					defer conn.Close()
					_ = conn.(*tls.Conn).Handshake()
				}()
				return ln.Addr().String()
			},
			wantErr: true,
		},
		{
			name: "invalid identity JSON returns unmarshal error",
			setupServer: func(t *testing.T) string {
				return newMTLSServer(t, pki, func(conn net.Conn) {
					defer conn.Close()
					session, err := outbound.Server(conn, nil)
					if err != nil {
						return
					}
					registry := outbound.NewRegistry()
					registry.Register(outbound.Upstream{
						Id:   shrd.IdentityUpstreamID,
						Name: "identity",
						Dial: func(ctx context.Context) (net.Conn, error) {
							local, remote := net.Pipe()
							go func() { _, _ = local.Write([]byte("not valid json")); _ = local.Close() }()
							return remote, nil
						},
					})
					tunnel := outbound.NewTunnel(session, registry)
					_ = tunnel.Serve(context.Background())
				})
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := tc.setupServer(t)
			a := newTestAgent(t, pki, addr)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			tunnel, identity, err := a.connect(ctx)
			if tunnel != nil {
				t.Cleanup(func() { _ = tunnel.Close() })
			}
			assertion(t, result{tunnel: tunnel, identity: identity, err: err}, tc.wantErr, tc.wantIdentity)
		})
	}
}
