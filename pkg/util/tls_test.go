package util

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testPKI struct {
	caCertFile string
	certFile   string
	keyFile    string
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

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-leaf"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	leafCertDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)

	return &testPKI{
		caCertFile: writePEM(t, "CERTIFICATE", caCertDER),
		certFile:   writePEM(t, "CERTIFICATE", leafCertDER),
		keyFile:    writeECKey(t, leafKey),
	}
}

func writePEM(t *testing.T, blockType string, der []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.pem")
	require.NoError(t, err)
	require.NoError(t, pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}))
	require.NoError(t, f.Close())
	return f.Name()
}

func writeECKey(t *testing.T, key *ecdsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return writePEM(t, "EC PRIVATE KEY", der)
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.pem")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

type result struct {
	cfg *tls.Config
	err error
}

func assertion(t *testing.T, got result, wantErr bool) {
	t.Helper()
	if wantErr {
		assert.Error(t, got.err)
		assert.Nil(t, got.cfg)
		return
	}
	assert.NoError(t, got.err)
	require.NotNil(t, got.cfg)
	assert.Len(t, got.cfg.Certificates, 1)
	assert.NotNil(t, got.cfg.RootCAs)
	assert.Equal(t, uint16(tls.VersionTLS12), got.cfg.MinVersion)
}

func TestSetupTLSConfig(t *testing.T) {
	p := newTestPKI(t)

	// A key that belongs to a different cert — triggers LoadX509KeyPair mismatch.
	mismatchedKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	mismatchedKeyFile := writeECKey(t, mismatchedKey)

	// A file whose content is not a valid PEM certificate.
	invalidCAFile := writeTempFile(t, "this is not a certificate")

	cases := []struct {
		name     string
		certPath string
		keyPath  string
		caPath   string
		wantErr  bool
	}{
		{
			name:     "valid cert, key, and CA returns config",
			certPath: p.certFile,
			keyPath:  p.keyFile,
			caPath:   p.caCertFile,
			wantErr:  false,
		},
		{
			name:     "missing cert file returns error",
			certPath: "/nonexistent/cert.pem",
			keyPath:  p.keyFile,
			caPath:   p.caCertFile,
			wantErr:  true,
		},
		{
			name:     "missing key file returns error",
			certPath: p.certFile,
			keyPath:  "/nonexistent/key.pem",
			caPath:   p.caCertFile,
			wantErr:  true,
		},
		{
			name:     "missing CA file returns error",
			certPath: p.certFile,
			keyPath:  p.keyFile,
			caPath:   "/nonexistent/ca.pem",
			wantErr:  true,
		},
		{
			name:     "invalid CA PEM returns error",
			certPath: p.certFile,
			keyPath:  p.keyFile,
			caPath:   invalidCAFile,
			wantErr:  true,
		},
		{
			name:     "mismatched cert and key returns error",
			certPath: p.certFile,
			keyPath:  mismatchedKeyFile,
			caPath:   p.caCertFile,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := SetupTLSConfig(tc.certPath, tc.keyPath, tc.caPath)
			assertion(t, result{cfg: cfg, err: err}, tc.wantErr)
		})
	}
}
