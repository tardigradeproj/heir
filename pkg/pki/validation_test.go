package pki

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCA(t *testing.T) Certificate {
	t.Helper()
	ca, err := GenerateSelfSignedCert()
	require.NoError(t, err)
	return *ca
}

func newLeaf(t *testing.T, ca Certificate, cn, o string, hostnames []string, expiry time.Duration) Certificate {
	t.Helper()
	leaf, err := SignCSR(ca, CSR{CN: cn, O: o, Hostnames: hostnames}, expiry)
	require.NoError(t, err)
	return *leaf
}

func TestParseAndVerifyCertificate(t *testing.T) {
	ca := newCA(t)
	otherCA := newCA(t)
	leaf := newLeaf(t, ca, "test-client", "testers", []string{"127.0.0.1", "test.example.com"}, time.Hour)
	unrelatedLeaf := newLeaf(t, ca, "unrelated", "testers", nil, time.Hour)

	tests := []struct {
		name      string
		prepare   func(t *testing.T) (certPEM []byte, ca Certificate, opts []VerifyOption)
		assertion func(t *testing.T, cert *x509.Certificate, err error)
	}{
		{
			name: "invalid PEM bytes",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return []byte("not a certificate"), ca, nil
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidPEM)
				assert.Nil(t, cert)
			},
		},
		{
			name: "unexpected PEM block type",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				// leaf.Key is a valid PEM block, but it's a private key, not a certificate.
				return leaf.Key, ca, nil
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidPEM)
				assert.Nil(t, cert)
			},
		},
		{
			name: "malformed certificate DER",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				bad := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")})
				return bad, ca, nil
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidPEM)
				assert.Nil(t, cert)
			},
		},
		{
			name: "valid certificate passes with default options",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, nil
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.NoError(t, err)
				require.NotNil(t, cert)
				assert.Equal(t, "test-client", cert.Subject.CommonName)
			},
		},
		{
			name: "organization mismatch",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, []VerifyOption{WithOValidation("wrong-org")}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrSubjectMismatch)
				assert.Nil(t, cert)
			},
		},
		{
			name: "organization match",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, []VerifyOption{WithOValidation("testers")}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.NoError(t, err)
				require.NotNil(t, cert)
			},
		},
		{
			name: "common name mismatch",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, []VerifyOption{WithCNValidation("wrong-cn")}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrSubjectMismatch)
				assert.Nil(t, cert)
			},
		},
		{
			name: "SANs mismatch",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, []VerifyOption{WithSANsValidation([]string{"other.example.com"})}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrSANMismatch)
				assert.Nil(t, cert)
			},
		},
		{
			name: "SANs match order-insensitively",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, []VerifyOption{WithSANsValidation([]string{"test.example.com", "127.0.0.1"})}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.NoError(t, err)
				require.NotNil(t, cert)
			},
		},
		{
			name: "private key does not match certificate",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, []VerifyOption{WithKeyValidation(unrelatedLeaf.Key)}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrKeyMismatch)
				assert.Nil(t, cert)
			},
		},
		{
			name: "private key matches certificate",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, []VerifyOption{WithKeyValidation(leaf.Key)}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.NoError(t, err)
				require.NotNil(t, cert)
			},
		},
		{
			name: "certificate not yet valid",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				return leaf.Cert, ca, []VerifyOption{WithClock(func() time.Time { return past })}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrExpired)
				assert.Nil(t, cert)
			},
		},
		{
			name: "certificate expired",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				future := time.Now().Add(48 * time.Hour)
				return leaf.Cert, ca, []VerifyOption{WithClock(func() time.Time { return future })}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrExpired)
				assert.Nil(t, cert)
			},
		},
		{
			name: "certificate does not chain to expected ca",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, otherCA, nil
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrUntrusted)
				assert.Nil(t, cert)
			},
		},
		{
			name: "certificate lacks required key usage",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, []VerifyOption{WithKeyUsages(x509.ExtKeyUsageCodeSigning)}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrUntrusted)
				assert.Nil(t, cert)
			},
		},
		{
			name: "certificate satisfies custom key usage",
			prepare: func(t *testing.T) ([]byte, Certificate, []VerifyOption) {
				return leaf.Cert, ca, []VerifyOption{WithKeyUsages(x509.ExtKeyUsageServerAuth)}
			},
			assertion: func(t *testing.T, cert *x509.Certificate, err error) {
				require.NoError(t, err)
				require.NotNil(t, cert)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			certPEM, ca, opts := tt.prepare(t)
			cert, err := ParseAndVerifyCertificate(certPEM, ca, opts...)
			tt.assertion(t, cert, err)
		})
	}
}
