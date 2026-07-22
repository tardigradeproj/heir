package pki

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cloudflare/cfssl/helpers"
)

// Sentinel errors allow callers to branch on failure mode with errors.Is.
var (
	ErrInvalidPEM      = errors.New("pki: certificate is not valid PEM")
	ErrKeyMismatch     = errors.New("pki: private key does not match certificate")
	ErrSubjectMismatch = errors.New("pki: certificate subject does not match expectation")
	ErrSANMismatch     = errors.New("pki: certificate SANs do not match expectation")
	ErrUntrusted       = errors.New("pki: certificate does not chain to the expected CA")
	ErrExpired         = errors.New("pki: certificate is outside its validity window")
)

// VerifyOption configures optional validation performed by ParseAndVerifyCertificate.
type VerifyOption func(*verifyOptions)

type verifyOptions struct {
	keyPEM     []byte
	org        *string
	commonName *string
	sans       []string
	keyUsages  []x509.ExtKeyUsage
	now        func() time.Time
}

func (o *verifyOptions) defaults() {
	if o.now == nil {
		o.now = time.Now
	}
	if o.keyUsages == nil {
		o.keyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
}

// WithKeyValidation checks that keyPEM is the private key matching the certificate's
// public key.
func WithKeyValidation(keyPEM []byte) VerifyOption {
	return func(o *verifyOptions) { o.keyPEM = keyPEM }
}

// WithOValidation checks that the certificate's subject organization includes org.
func WithOValidation(org string) VerifyOption {
	return func(o *verifyOptions) { o.org = &org }
}

// WithCNValidation checks that the certificate's subject common name equals cn.
func WithCNValidation(cn string) VerifyOption {
	return func(o *verifyOptions) { o.commonName = &cn }
}

// WithSANsValidation checks that the certificate's DNS and IP SANs exactly match sans,
// order-insensitively. Duplicates are significant.
func WithSANsValidation(sans []string) VerifyOption {
	return func(o *verifyOptions) { o.sans = sans }
}

// WithKeyUsages overrides the extended key usages required during chain verification.
// Defaults to client auth.
func WithKeyUsages(usages ...x509.ExtKeyUsage) VerifyOption {
	return func(o *verifyOptions) { o.keyUsages = usages }
}

// WithClock overrides the time source used for validity checks. Intended for tests.
func WithClock(now func() time.Time) VerifyOption {
	return func(o *verifyOptions) { o.now = now }
}

// ParseAndVerifyCertificate decodes a PEM-encoded certificate and validates it against
// the given root CA. The certificate must chain to ca.Cert for the configured key
// usages and be within its validity window. Returns the parsed certificate so callers
// can inspect further fields without re-parsing.
//
// Errors wrap the sentinels above; use errors.Is to distinguish failure modes.
func ParseAndVerifyCertificate(certPEM []byte, ca Certificate, opts ...VerifyOption) (*x509.Certificate, error) {
	options := &verifyOptions{}
	for _, opt := range opts {
		opt(options)
	}
	options.defaults()

	cert, err := parseCertificatePEM(certPEM)
	if err != nil {
		return nil, err
	}

	// Cheap, deterministic checks first; chain verification last.
	if err := verifySubject(cert, options); err != nil {
		return nil, err
	}
	if err := verifySANs(cert, options); err != nil {
		return nil, err
	}
	if err := verifyKeyPair(cert, options.keyPEM); err != nil {
		return nil, err
	}
	if err := verifyValidity(cert, options.now()); err != nil {
		return nil, err
	}
	if err := verifyChain(cert, ca, options.keyUsages); err != nil {
		return nil, err
	}
	return cert, nil
}

func parseCertificatePEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, ErrInvalidPEM
	}
	if block.Type != "" && block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: unexpected PEM block type %q", ErrInvalidPEM, block.Type)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPEM, err)
	}
	return cert, nil
}

func verifySubject(cert *x509.Certificate, o *verifyOptions) error {
	if o.org != nil && !slices.Contains(cert.Subject.Organization, *o.org) {
		return fmt.Errorf("%w: organization %v does not include %q",
			ErrSubjectMismatch, cert.Subject.Organization, *o.org)
	}
	if o.commonName != nil && cert.Subject.CommonName != *o.commonName {
		return fmt.Errorf("%w: CN %q, want %q",
			ErrSubjectMismatch, cert.Subject.CommonName, *o.commonName)
	}
	return nil
}

func verifySANs(cert *x509.Certificate, o *verifyOptions) error {
	if o.sans == nil {
		return nil
	}
	got := certSANs(cert)
	want := slices.Clone(o.sans)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("%w: got %v, want %v", ErrSANMismatch, got, want)
	}
	return nil
}

func verifyKeyPair(cert *x509.Certificate, keyPEM []byte) error {
	if len(keyPEM) == 0 {
		return nil
	}
	key, err := helpers.ParsePrivateKeyPEM(keyPEM)
	if err != nil {
		return fmt.Errorf("%w: parse private key: %w", ErrKeyMismatch, err)
	}
	pub, ok := cert.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok {
		return fmt.Errorf("%w: public key of type %T does not support comparison",
			ErrKeyMismatch, cert.PublicKey)
	}
	if !pub.Equal(key.Public()) {
		return ErrKeyMismatch
	}
	return nil
}

func verifyValidity(cert *x509.Certificate, now time.Time) error {
	switch {
	case now.Before(cert.NotBefore):
		return fmt.Errorf("%w: not valid until %s", ErrExpired, cert.NotBefore.UTC().Format(time.RFC3339))
	case now.After(cert.NotAfter):
		return fmt.Errorf("%w: expired at %s", ErrExpired, cert.NotAfter.UTC().Format(time.RFC3339))
	default:
		return nil
	}
}

func verifyChain(cert *x509.Certificate, ca Certificate, usages []x509.ExtKeyUsage) error {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.Cert) {
		return fmt.Errorf("%w: CA certificate is not valid PEM", ErrUntrusted)
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: usages,
	}); err != nil {
		return fmt.Errorf("%w: %w", ErrUntrusted, err)
	}
	return nil
}

// certSANs returns the certificate's DNS name and IP address SANs as a single list.
func certSANs(cert *x509.Certificate) []string {
	sans := make([]string, 0, len(cert.DNSNames)+len(cert.IPAddresses))
	sans = append(sans, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}
	return sans
}
