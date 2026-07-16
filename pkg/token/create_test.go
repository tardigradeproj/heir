package token

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func generateTestCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err, "generating test CA key")
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err, "creating test CA certificate")
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// writeKubeconfig writes a kubeconfig file pointing at serverURL with the given CA
// data and returns the file path. The file is removed via t.Cleanup.
func writeKubeconfig(t *testing.T, serverURL, contextName string, caData []byte) string {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[contextName] = &clientcmdapi.Cluster{
		Server:                   serverURL,
		CertificateAuthorityData: caData,
		InsecureSkipTLSVerify:    true, // plain HTTP test server
	}
	cfg.AuthInfos[contextName] = &clientcmdapi.AuthInfo{}
	cfg.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  contextName,
		AuthInfo: contextName,
	}
	cfg.CurrentContext = contextName

	f, err := os.CreateTemp("", "kubeconfig-*.yaml")
	require.NoError(t, err, "creating temp kubeconfig")
	require.NoError(t, clientcmd.WriteToFile(*cfg, f.Name()), "writing kubeconfig")
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

// fakeAPIServer returns an httptest.Server that handles the two endpoints
// CreateBootstrapToken exercises:
//   - GET  .../namespaces/kube-system/configmaps/worker-profile  — always 200;
//     the "control.plane.endpoint" key holds a ControlPlaneExternalEndpointSpec JSON
//     with apiServer.host set to externalHost and port 6443.
//   - POST .../namespaces/kube-system/secrets — responds with secretStatusCode.
func fakeAPIServer(t *testing.T, secretStatusCode int, externalHost string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/namespaces/kube-system/configmaps/worker-profile"):
			nodeProfile := map[string]interface{}{
				"apiServer": map[string]interface{}{
					"host": externalHost,
					"port": 6443,
				},
			}
			nodeProfileJSON, _ := json.Marshal(nodeProfile)
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-profile", Namespace: "kube-system"},
				Data:       map[string]string{"control.plane.endpoint": string(nodeProfileJSON)},
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(cm)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/namespaces/kube-system/secrets"):
			w.WriteHeader(secretStatusCode)
			if secretStatusCode == http.StatusCreated {
				_ = json.NewEncoder(w).Encode(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-token-test", Namespace: "kube-system"},
				})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestCreateBootstrapToken(t *testing.T) {
	caPEM := generateTestCA(t)

	tests := []struct {
		name        string
		setup       func(t *testing.T) (kubeconfig, contextName string)
		expiry      time.Duration
		wantErr     bool
		wantErrMsg  string
		validateB64 func(t *testing.T, b64 string)
	}{
		{
			name: "returns valid base64-encoded bootstrap kubeconfig on success",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusCreated, "bootstrap-token-test")
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", caPEM), "heir"
			},
			expiry:  1 * time.Hour,
			wantErr: false,
			validateB64: func(t *testing.T, b64 string) {
				raw, err := base64.StdEncoding.DecodeString(b64)
				require.NoError(t, err, "result is not valid base64")
				cfg, err := clientcmd.Load(raw)
				require.NoError(t, err, "decoded value is not a valid kubeconfig")
				assert.NotEmpty(t, cfg.CurrentContext, "bootstrap kubeconfig has no current-context")
				ctx := cfg.Contexts[cfg.CurrentContext]
				require.NotNil(t, ctx, "current-context %q not found in bootstrap kubeconfig", cfg.CurrentContext)
				user := cfg.AuthInfos[ctx.AuthInfo]
				require.NotNil(t, user, "auth info %q not found", ctx.AuthInfo)
				assert.NotEmpty(t, user.Token, "bootstrap kubeconfig has no token in user credentials")
				assert.Contains(t, user.Token, ".", "token %q does not match <id>.<secret> format", user.Token)
			},
		},
		{
			name: "bootstrap kubeconfig carries external address as server URL and CA data from kubeconfig",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusCreated, "127.0.0.1")
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", caPEM), "heir"
			},
			expiry:  30 * time.Minute,
			wantErr: false,
			validateB64: func(t *testing.T, b64 string) {
				raw, err := base64.StdEncoding.DecodeString(b64)
				require.NoError(t, err)
				cfg, err := clientcmd.Load(raw)
				require.NoError(t, err)
				ctx, ok := cfg.Contexts[cfg.CurrentContext]
				require.True(t, ok, "current-context %q not found in bootstrap kubeconfig", cfg.CurrentContext)
				cluster, ok := cfg.Clusters[ctx.Cluster]
				require.True(t, ok, "cluster %q not found in bootstrap kubeconfig", ctx.Cluster)
				assert.Equal(t, "https://127.0.0.1:6443", cluster.Server)
				assert.NotEmpty(t, cluster.CertificateAuthorityData, "bootstrap kubeconfig cluster has no CA data")
			},
		},
		{
			name: "non-default context name is respected",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusCreated, "127.0.0.1")
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "my-cluster", caPEM), "my-cluster"
			},
			expiry:  30 * time.Minute,
			wantErr: false,
			validateB64: func(t *testing.T, b64 string) {
				_, err := base64.StdEncoding.DecodeString(b64)
				require.NoError(t, err, "result is not valid base64")
			},
		},
		{
			// extractClusterInfo calls RawConfig before ClientConfig, so a missing
			// file is caught there first.
			name: "kubeconfig file does not exist",
			setup: func(t *testing.T) (string, string) {
				return "/nonexistent/kubeconfig.yaml", "heir"
			},
			expiry:     1 * time.Hour,
			wantErr:    true,
			wantErrMsg: "error to read kubeconfig",
		},
		{
			name: "context not present in kubeconfig",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusCreated, "")
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", caPEM), "missing"
			},
			expiry:     1 * time.Hour,
			wantErr:    true,
			wantErrMsg: "context \"missing\" not found in kubeconfig",
		},
		{
			name: "kubeconfig cluster has no CA data",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusCreated, "")
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", nil), "heir"
			},
			expiry:     1 * time.Hour,
			wantErr:    true,
			wantErrMsg: "has no embedded CA data",
		},
		{
			name: "api server returns error on secret creation",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusInternalServerError, "127.0.0.1")
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", caPEM), "heir"
			},
			expiry:     1 * time.Hour,
			wantErr:    true,
			wantErrMsg: "failed to create bootstrap token secret",
		},
		{
			name: "external host from configmap becomes the single bootstrap cluster entry",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusCreated, "10.0.0.1")
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", caPEM), "heir"
			},
			expiry:  1 * time.Hour,
			wantErr: false,
			validateB64: func(t *testing.T, b64 string) {
				raw, err := base64.StdEncoding.DecodeString(b64)
				require.NoError(t, err)
				cfg, err := clientcmd.Load(raw)
				require.NoError(t, err)
				assert.Len(t, cfg.Clusters, 1, "expected exactly 1 cluster entry")
				cluster, ok := cfg.Clusters["bootstrap"]
				require.True(t, ok, "cluster entry \"bootstrap\" not found in bootstrap kubeconfig")
				assert.Equal(t, "https://10.0.0.1:6443", cluster.Server)
				assert.NotEmpty(t, cluster.CertificateAuthorityData, "cluster has no CA data")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kubeconfig, contextName := tc.setup(t)

			b64, err := CreateBootstrapToken(context.Background(), kubeconfig, contextName, tc.expiry)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrMsg != "" {
					assert.ErrorContains(t, err, tc.wantErrMsg)
				}
				return
			}
			require.NoError(t, err)
			if tc.validateB64 != nil {
				tc.validateB64(t, b64)
			}
		})
	}
}
