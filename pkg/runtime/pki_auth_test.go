package runtime

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
	"github.com/tardigrade-runtime/samaritano/pkg/pki"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
)

func TestAPIServerAltNames(t *testing.T) {
	tests := []struct {
		name     string
		spec     controlplanev1alpha1.APIServerSpec
		expected []string
	}{
		{
			name: "defaults only when no extra SANs provided",
			spec: controlplanev1alpha1.APIServerSpec{},
			expected: []string{
				"127.0.0.1",
				"api-server.kubernetes.local",
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.cluster",
				"kubernetes.default.svc",
				"server.kubernetes.local",
			},
		},
		{
			name: "extra SANs are merged with defaults",
			spec: controlplanev1alpha1.APIServerSpec{
				Sans: []string{"my-cluster.example.com", "10.0.0.1"},
			},
			expected: []string{
				"10.0.0.1",
				"127.0.0.1",
				"api-server.kubernetes.local",
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.cluster",
				"kubernetes.default.svc",
				"my-cluster.example.com",
				"server.kubernetes.local",
			},
		},
		{
			name: "externalAddress hostname is extracted and added",
			spec: controlplanev1alpha1.APIServerSpec{
				ExternalAddress: "https://my-cluster.example.com:6443",
			},
			expected: []string{
				"127.0.0.1",
				"api-server.kubernetes.local",
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.cluster",
				"kubernetes.default.svc",
				"my-cluster.example.com",
				"server.kubernetes.local",
			},
		},
		{
			name: "duplicate SANs are deduplicated",
			spec: controlplanev1alpha1.APIServerSpec{
				Sans:            []string{"kubernetes", "127.0.0.1"},
				ExternalAddress: "https://kubernetes:6443",
			},
			expected: []string{
				"127.0.0.1",
				"api-server.kubernetes.local",
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.cluster",
				"kubernetes.default.svc",
				"server.kubernetes.local",
			},
		},
		{
			name: "empty SANs are removed",
			spec: controlplanev1alpha1.APIServerSpec{
				Sans: []string{"", "valid.example.com", ""},
			},
			expected: []string{
				"127.0.0.1",
				"api-server.kubernetes.local",
				"kubernetes",
				"kubernetes.default",
				"kubernetes.default.cluster",
				"kubernetes.default.svc",
				"server.kubernetes.local",
				"valid.example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := APIServerAltNames(tt.spec)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func parseCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	require.NotNil(t, block, "expected valid PEM block")
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return cert
}

func pkiAuthRuntime(name, namespace string, apiserver controlplanev1alpha1.APIServerSpec) *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: controlplanev1alpha1.RuntimeSpec{
			UpstreamCluster: controlplanev1alpha1.UpstreamCluster{
				APIServer: apiserver,
			},
		},
	}
}

func TestGeneratePKIAuthSecret(t *testing.T) {
	layout := NewControlPlaneLayout()

	tests := []struct {
		name      string
		runtime   *controlplanev1alpha1.Runtime
		validate  func(t *testing.T, secret map[string][]byte)
	}{
		{
			name:    "secret name and namespace match the runtime",
			runtime: pkiAuthRuntime("my-cluster", "default", controlplanev1alpha1.APIServerSpec{}),
			validate: func(t *testing.T, _ map[string][]byte) {},
		},
		{
			name:    "all PKI keys are present",
			runtime: pkiAuthRuntime("my-cluster", "default", controlplanev1alpha1.APIServerSpec{}),
			validate: func(t *testing.T, data map[string][]byte) {
				for _, key := range []string{
					layout.PKI.CACert.SecretKey,
					layout.PKI.CAKey.SecretKey,
					layout.PKI.APIServerCert.SecretKey,
					layout.PKI.APIServerKey.SecretKey,
					layout.PKI.ServiceAccountCert.SecretKey,
					layout.PKI.ServiceAccountKey.SecretKey,
				} {
					assert.Contains(t, data, key, "missing PKI key %q", key)
					assert.NotEmpty(t, data[key], "empty value for PKI key %q", key)
				}
			},
		},
		{
			name:    "all auth kubeconfig keys are present and valid",
			runtime: pkiAuthRuntime("my-cluster", "default", controlplanev1alpha1.APIServerSpec{}),
			validate: func(t *testing.T, data map[string][]byte) {
				for _, key := range []string{
					layout.Auth.AdminConf.SecretKey,
					layout.Auth.ControllerManagerConf.SecretKey,
					layout.Auth.SchedulerConf.SecretKey,
				} {
					require.Contains(t, data, key, "missing auth key %q", key)
					cfg, err := clientcmd.Load(data[key])
					require.NoError(t, err, "invalid kubeconfig for key %q", key)
					assert.NotEmpty(t, cfg.CurrentContext)
				}
			},
		},
		{
			name:    "CA certificate is valid PEM and currently valid",
			runtime: pkiAuthRuntime("my-cluster", "default", controlplanev1alpha1.APIServerSpec{}),
			validate: func(t *testing.T, data map[string][]byte) {
				now := time.Now()
				ca := parseCert(t, data[layout.PKI.CACert.SecretKey])
				assert.True(t, ca.IsCA, "expected CA flag to be set")
				assert.True(t, now.After(ca.NotBefore), "CA cert not yet valid")
				assert.True(t, now.Before(ca.NotAfter), "CA cert already expired")
			},
		},
		{
			name:    "component certificates have correct duration, are currently valid, and are signed by the CA",
			runtime: pkiAuthRuntime("my-cluster", "default", controlplanev1alpha1.APIServerSpec{}),
			validate: func(t *testing.T, data map[string][]byte) {
				now := time.Now()
				ca := parseCert(t, data[layout.PKI.CACert.SecretKey])
				pool := x509.NewCertPool()
				pool.AddCert(ca)

				for _, key := range []string{
					layout.PKI.APIServerCert.SecretKey,
					layout.PKI.ServiceAccountCert.SecretKey,
				} {
					cert := parseCert(t, data[key])
					assert.True(t, now.After(cert.NotBefore), "cert %q not yet valid", key)
					assert.True(t, now.Before(cert.NotAfter), "cert %q already expired", key)
					duration := cert.NotAfter.Sub(cert.NotBefore)
					assert.WithinDuration(t, cert.NotBefore.Add(CertificateDuration), cert.NotAfter, time.Minute,
						"cert %q duration deviates from CertificateDuration (got %v)", key, duration)
					_, err := cert.Verify(x509.VerifyOptions{Roots: pool})
					assert.NoError(t, err, "cert %q failed chain verification", key)
				}
			},
		},
		{
			name: "apiserver cert includes externalAddress hostname as SAN",
			runtime: pkiAuthRuntime("my-cluster", "default", controlplanev1alpha1.APIServerSpec{
				ExternalAddress: "https://my-cluster.example.com:6443",
			}),
			validate: func(t *testing.T, data map[string][]byte) {
				block, _ := pem.Decode(data[layout.PKI.APIServerCert.SecretKey])
				require.NotNil(t, block)
				cert, err := x509.ParseCertificate(block.Bytes)
				require.NoError(t, err)
				var sans []string
				sans = append(sans, cert.DNSNames...)
				for _, ip := range cert.IPAddresses {
					sans = append(sans, ip.String())
				}
				assert.Contains(t, sans, "my-cluster.example.com")
			},
		},
		{
			name:    "admin kubeconfig current context uses runtime name",
			runtime: pkiAuthRuntime("prod-cluster", "production", controlplanev1alpha1.APIServerSpec{}),
			validate: func(t *testing.T, data map[string][]byte) {
				cfg, err := clientcmd.Load(data[layout.Auth.AdminConf.SecretKey])
				require.NoError(t, err)
				assert.Equal(t, "samaritano-prod-cluster@kubernetes", cfg.CurrentContext)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, err := GeneratePKIAuthSecret(tt.runtime, layout)
			require.NoError(t, err)
			require.NotNil(t, secret)
			assert.Equal(t, fmt.Sprintf("%s-pki-auth", tt.runtime.Name), secret.Name)
			assert.Equal(t, tt.runtime.Namespace, secret.Namespace)
			tt.validate(t, secret.Data)
		})
	}
}

func TestGenerateKubeconfig(t *testing.T) {
	ca, err := pki.GenerateSelfSignedCert()
	require.NoError(t, err)

	cert, err := pki.SignCSR(*ca, pki.CSR{
		CN:        "kubernetes-admin",
		O:         "system:masters",
		Hostnames: []string{},
	}, CertificateDuration)
	require.NoError(t, err)

	tests := []struct {
		name     string
		username string
	}{
		{
			name:     "admin kubeconfig has correct context and cluster",
			username: "kubernetes-admin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := generateKubeconfig(tt.username, ca.Cert, cert)
			require.NoError(t, err)
			require.NotEmpty(t, raw)

			cfg, err := clientcmd.Load(raw)
			require.NoError(t, err)

			expectedContext := fmt.Sprintf("%s@kubernetes", tt.username)

			assert.Equal(t, expectedContext, cfg.CurrentContext)

			ctx, ok := cfg.Contexts[expectedContext]
			require.True(t, ok, "context %q not found", expectedContext)
			assert.Equal(t, "kubernetes", ctx.Cluster)
			assert.Equal(t, tt.username, ctx.AuthInfo)

			cluster, ok := cfg.Clusters["kubernetes"]
			require.True(t, ok, "cluster 'kubernetes' not found")
			assert.Equal(t, "https://127.0.0.1:6443", cluster.Server)
			assert.Equal(t, ca.Cert, cluster.CertificateAuthorityData)

			authInfo, ok := cfg.AuthInfos[tt.username]
			require.True(t, ok, "auth info %q not found", tt.username)
			assert.Equal(t, cert.Cert, authInfo.ClientCertificateData)
			assert.Equal(t, cert.Key, authInfo.ClientKeyData)
		})
	}
}
