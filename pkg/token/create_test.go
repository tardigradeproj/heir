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
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func generateTestCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating test CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating test CA certificate: %v", err)
	}
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
	if err != nil {
		t.Fatalf("creating temp kubeconfig: %v", err)
	}
	if err := clientcmd.WriteToFile(*cfg, f.Name()); err != nil {
		t.Fatalf("writing kubeconfig: %v", err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

// fakeAPIServer returns an httptest.Server that handles the two endpoints
// CreateBootstrapToken exercises:
//   - GET  .../namespaces/kube-system/configmaps/worker-profile  — always 200;
//     the "control.plane.endpoint" key holds a NodeProfile JSON with the given addresses and port 6443.
//   - POST .../namespaces/kube-system/secrets — responds with secretStatusCode.
func fakeAPIServer(t *testing.T, secretStatusCode int, externalAddresses []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/namespaces/kube-system/configmaps/worker-profile"):
			addrs := externalAddresses
			if addrs == nil {
				addrs = []string{}
			}
			nodeProfile := map[string]interface{}{
				"controlPlaneEndpoint": map[string]interface{}{
					"addresses": addrs,
					"apiServer": map[string]interface{}{"port": 6443},
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
				srv := fakeAPIServer(t, http.StatusCreated, []string{"bootstrap-token-test"})
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", caPEM), "heir"
			},
			expiry:  1 * time.Hour,
			wantErr: false,
			validateB64: func(t *testing.T, b64 string) {
				raw, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					t.Fatalf("result is not valid base64: %v", err)
				}
				cfg, err := clientcmd.Load(raw)
				if err != nil {
					t.Fatalf("decoded value is not a valid kubeconfig: %v", err)
				}
				if cfg.CurrentContext == "" {
					t.Error("bootstrap kubeconfig has no current-context")
				}
				ctx := cfg.Contexts[cfg.CurrentContext]
				if ctx == nil {
					t.Fatalf("current-context %q not found in bootstrap kubeconfig", cfg.CurrentContext)
				}
				user := cfg.AuthInfos[ctx.AuthInfo]
				if user == nil || user.Token == "" {
					t.Error("bootstrap kubeconfig has no token in user credentials")
				}
				if !strings.Contains(user.Token, ".") {
					t.Errorf("token %q does not match <id>.<secret> format", user.Token)
				}
			},
		},
		{
			name: "bootstrap kubeconfig carries cluster server URL and CA data",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusCreated, []string{"127.0.0.1"})
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", caPEM), "heir"
			},
			expiry:  30 * time.Minute,
			wantErr: false,
			validateB64: func(t *testing.T, b64 string) {
				raw, _ := base64.StdEncoding.DecodeString(b64)
				cfg, _ := clientcmd.Load(raw)
				ctx := cfg.Contexts[cfg.CurrentContext]
				cluster := cfg.Clusters[ctx.Cluster]
				if cluster.Server == "" {
					t.Error("bootstrap kubeconfig cluster has no server URL")
				}
				if len(cluster.CertificateAuthorityData) == 0 {
					t.Error("bootstrap kubeconfig cluster has no CA data")
				}
			},
		},
		{
			name: "non-default context name is respected",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusCreated, []string{"127.0.0.1"})
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "my-cluster", caPEM), "my-cluster"
			},
			expiry:  30 * time.Minute,
			wantErr: false,
			validateB64: func(t *testing.T, b64 string) {
				if _, err := base64.StdEncoding.DecodeString(b64); err != nil {
					t.Fatalf("result is not valid base64: %v", err)
				}
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
				srv := fakeAPIServer(t, http.StatusCreated, nil)
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
				srv := fakeAPIServer(t, http.StatusCreated, nil)
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
				srv := fakeAPIServer(t, http.StatusInternalServerError, []string{"127.0.0.1"})
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", caPEM), "heir"
			},
			expiry:     1 * time.Hour,
			wantErr:    true,
			wantErrMsg: "failed to create bootstrap token secret",
		},
		{
			// external addresses are returned as https://<host>:<port> under bootstrap-1, bootstrap-2, …
			// addresses that equal the primary server URL are deduplicated and skipped.
			name: "external addresses from configmap appear as bootstrap-N cluster entries",
			setup: func(t *testing.T) (string, string) {
				srv := fakeAPIServer(t, http.StatusCreated, []string{"10.0.0.1", "10.0.0.2"})
				t.Cleanup(srv.Close)
				return writeKubeconfig(t, srv.URL, "heir", caPEM), "heir"
			},
			expiry:  1 * time.Hour,
			wantErr: false,
			validateB64: func(t *testing.T, b64 string) {
				raw, _ := base64.StdEncoding.DecodeString(b64)
				cfg, _ := clientcmd.Load(raw)
				// Expect primary "bootstrap" cluster + one entry per external address.
				if got := len(cfg.Clusters); got != 3 {
					t.Errorf("expected 3 cluster entries (1 primary + 2 external), got %d", got)
				}
				for i, addr := range []string{"https://10.0.0.1:6443", "https://10.0.0.2:6443"} {
					name := fmt.Sprintf("bootstrap-%d", i+1)
					cluster, ok := cfg.Clusters[name]
					if !ok {
						t.Errorf("expected cluster entry %q not found in bootstrap kubeconfig", name)
						continue
					}
					if cluster.Server != addr {
						t.Errorf("cluster %q server = %q, want %q", name, cluster.Server, addr)
					}
					if len(cluster.CertificateAuthorityData) == 0 {
						t.Errorf("cluster %q has no CA data", name)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kubeconfig, contextName := tc.setup(t)

			b64, err := CreateBootstrapToken(context.Background(), kubeconfig, contextName, tc.expiry)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.wantErrMsg != "" && !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErrMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.validateB64 != nil {
				tc.validateB64(t, b64)
			}
		})
	}
}
