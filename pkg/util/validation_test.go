package util

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tardigradeproj/heir/pkg/pki"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func newTestCA(t *testing.T) pki.Certificate {
	t.Helper()
	ca, err := pki.GenerateSelfSignedCert()
	require.NoError(t, err)
	return *ca
}

func newTestLeaf(t *testing.T, ca pki.Certificate, cn, o string) pki.Certificate {
	t.Helper()
	leaf, err := pki.SignCSR(ca, pki.CSR{CN: cn, O: o, Hostnames: []string{"127.0.0.1"}}, time.Hour)
	require.NoError(t, err)
	return *leaf
}

// newKubeconfigBytes builds a minimal single-cluster/single-user kubeconfig, applies
// mutate (if given) to the in-memory config, and returns it serialized.
func newKubeconfigBytes(t *testing.T, server string, caCert []byte, authInfo *clientcmdapi.AuthInfo, mutate func(cfg *clientcmdapi.Config)) []byte {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["test-cluster"] = &clientcmdapi.Cluster{
		Server:                   server,
		CertificateAuthorityData: caCert,
	}
	cfg.AuthInfos["test-user"] = authInfo
	cfg.Contexts["test-context"] = &clientcmdapi.Context{
		Cluster:  "test-cluster",
		AuthInfo: "test-user",
	}
	cfg.CurrentContext = "test-context"
	if mutate != nil {
		mutate(cfg)
	}
	raw, err := clientcmd.Write(*cfg)
	require.NoError(t, err)
	return raw
}

func TestParseAndVerifyKubeconfig(t *testing.T) {
	ca := newTestCA(t)
	otherCA := newTestCA(t)
	leaf := newTestLeaf(t, ca, "test-client", "testers")
	unrelatedLeaf := newTestLeaf(t, ca, "unrelated", "testers")

	const wantServer = "https://api.example.com:6443"

	tests := []struct {
		name      string
		prepare   func(t *testing.T) ([]byte, []VerifyOption)
		assertion func(t *testing.T, kc *Kubeconfig, err error)
	}{
		{
			name: "invalid kubeconfig bytes",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				return []byte("not a kubeconfig"), nil
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidKubeconfig)
				assert.Nil(t, kc)
			},
		},
		{
			name: "missing current context",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, wantServer, ca.Cert, &clientcmdapi.AuthInfo{
					ClientCertificateData: leaf.Cert,
					ClientKeyData:         leaf.Key,
				}, func(cfg *clientcmdapi.Config) {
					cfg.CurrentContext = "missing-context"
				})
				return raw, nil
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrMissingContext)
				assert.Nil(t, kc)
			},
		},
		{
			name: "missing cluster",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, wantServer, ca.Cert, &clientcmdapi.AuthInfo{
					ClientCertificateData: leaf.Cert,
					ClientKeyData:         leaf.Key,
				}, func(cfg *clientcmdapi.Config) {
					cfg.Contexts["test-context"].Cluster = "missing-cluster"
				})
				return raw, nil
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrMissingCluster)
				assert.Nil(t, kc)
			},
		},
		{
			name: "missing auth info",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, wantServer, ca.Cert, &clientcmdapi.AuthInfo{
					ClientCertificateData: leaf.Cert,
					ClientKeyData:         leaf.Key,
				}, func(cfg *clientcmdapi.Config) {
					cfg.Contexts["test-context"].AuthInfo = "missing-user"
				})
				return raw, nil
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrMissingAuthInfo)
				assert.Nil(t, kc)
			},
		},
		{
			name: "missing client certificate data",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, wantServer, ca.Cert, &clientcmdapi.AuthInfo{
					ClientKeyData: leaf.Key,
				}, nil)
				return raw, nil
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrMissingClientCert)
				assert.Nil(t, kc)
			},
		},
		{
			name: "server does not match expectation",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, "https://wrong.example.com:6443", ca.Cert, &clientcmdapi.AuthInfo{
					ClientCertificateData: leaf.Cert,
					ClientKeyData:         leaf.Key,
				}, nil)
				return raw, []VerifyOption{WithServerValidation(wantServer)}
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrServerMismatch)
				assert.Nil(t, kc)
			},
		},
		{
			name: "server matches expectation",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, wantServer, ca.Cert, &clientcmdapi.AuthInfo{
					ClientCertificateData: leaf.Cert,
					ClientKeyData:         leaf.Key,
				}, nil)
				return raw, []VerifyOption{WithServerValidation(wantServer)}
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.NoError(t, err)
				require.NotNil(t, kc)
				assert.Equal(t, wantServer, kc.Cluster.Server)
			},
		},
		{
			name: "client certificate valid against ca",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, wantServer, ca.Cert, &clientcmdapi.AuthInfo{
					ClientCertificateData: leaf.Cert,
					ClientKeyData:         leaf.Key,
				}, nil)
				return raw, []VerifyOption{WithClientCertificateValidation(ca)}
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.NoError(t, err)
				require.NotNil(t, kc)
			},
		},
		{
			name: "client certificate does not chain to expected ca",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, wantServer, ca.Cert, &clientcmdapi.AuthInfo{
					ClientCertificateData: leaf.Cert,
					ClientKeyData:         leaf.Key,
				}, nil)
				return raw, []VerifyOption{WithClientCertificateValidation(otherCA)}
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidClientCert)
				assert.ErrorIs(t, err, pki.ErrUntrusted)
				assert.Nil(t, kc)
			},
		},
		{
			name: "client key does not match certificate",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, wantServer, ca.Cert, &clientcmdapi.AuthInfo{
					ClientCertificateData: leaf.Cert,
					ClientKeyData:         unrelatedLeaf.Key,
				}, nil)
				return raw, []VerifyOption{WithClientCertificateValidation(ca)}
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidClientCert)
				assert.ErrorIs(t, err, pki.ErrKeyMismatch)
				assert.Nil(t, kc)
			},
		},
		{
			name: "client certificate rejects unexpected organization",
			prepare: func(t *testing.T) ([]byte, []VerifyOption) {
				raw := newKubeconfigBytes(t, wantServer, ca.Cert, &clientcmdapi.AuthInfo{
					ClientCertificateData: leaf.Cert,
					ClientKeyData:         leaf.Key,
				}, nil)
				return raw, []VerifyOption{WithClientCertificateValidation(ca, pki.WithOValidation("unexpected-org"))}
			},
			assertion: func(t *testing.T, kc *Kubeconfig, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidClientCert)
				assert.ErrorIs(t, err, pki.ErrSubjectMismatch)
				assert.Nil(t, kc)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, opts := tt.prepare(t)
			kc, err := ParseAndVerifyKubeconfig(raw, opts...)
			tt.assertion(t, kc, err)
		})
	}
}
