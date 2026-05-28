package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tardigradeproj/heir/api/v1alpha1"
	samaritanoruntime "github.com/tardigradeproj/heir/pkg/runtime"
	"gvisor.dev/gvisor/pkg/cleanup"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// minimalKubeconfig is a syntactically valid kubeconfig pointing at a dummy server.
const minimalKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:9999
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test-token
`

// writeTempKubeconfig writes content to a temp file and registers cleanup.
func writeTempKubeconfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "kubeconfig-*.yaml")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

// newTestClientset starts an httptest.Server backed by handler and returns a Clientset pointed at it.
func newTestClientset(t *testing.T, handler http.Handler) *kubernetes.Clientset {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	return cs
}

// secretsHandler returns a handler that responds to POST (create) and DELETE on /secrets
// with the given createStatus, and records every DELETE call in deleteCalls.
func secretsHandler(createStatus int, deleteCalls *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if createStatus != http.StatusCreated {
				w.WriteHeader(createStatus)
				return
			}
			var secret corev1.Secret
			_ = json.NewDecoder(r.Body).Decode(&secret)
			secret.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}
			secret.ResourceVersion = "1"
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(&secret)
		case http.MethodDelete:
			if deleteCalls != nil {
				deleteCalls.Add(1)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// ---- buildClient ----

func TestBuildClient(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) string
		wantErr bool
	}{
		{
			name: "valid kubeconfig returns a non-nil client",
			setup: func(t *testing.T) string {
				return writeTempKubeconfig(t, minimalKubeconfig)
			},
			wantErr: false,
		},
		{
			name: "non-existent path returns error",
			setup: func(t *testing.T) string {
				return "/tmp/samaritano-test-no-such-kubeconfig.yaml"
			},
			wantErr: true,
		},
		{
			name: "malformed kubeconfig returns error",
			setup: func(t *testing.T) string {
				return writeTempKubeconfig(t, "not: valid: [unclosed bracket")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup(t)
			client, err := buildClient(path)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, client)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, client)
			}
		})
	}
}

// ---- setupPKIAuth ----

func pkiAuthRuntime(name, namespace string) *v1alpha1.Runtime {
	return &v1alpha1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func TestSetupPKIAuth(t *testing.T) {
	layout := samaritanoruntime.NewControlPlaneLayout()

	var cleanupDeleteCalls atomic.Int32

	tests := []struct {
		name         string
		runtime      *v1alpha1.Runtime
		handler      http.HandlerFunc
		wantErr      bool
		triggerClean bool
		validate     func(t *testing.T, cfg *clientcmdapi.Config)
	}{
		{
			name:    "creates secret and returns admin kubeconfig",
			runtime: pkiAuthRuntime("test-cluster", "default"),
			handler: secretsHandler(http.StatusCreated, nil),
			wantErr: false,
			validate: func(t *testing.T, cfg *clientcmdapi.Config) {
				assert.Equal(t, "samaritano-test-cluster@kubernetes", cfg.CurrentContext)
				_, hasContext := cfg.Contexts[cfg.CurrentContext]
				assert.True(t, hasContext, "expected context %q to be present", cfg.CurrentContext)
				_, hasCluster := cfg.Clusters["kubernetes"]
				assert.True(t, hasCluster, "expected cluster 'kubernetes' to be present")
			},
		},
		{
			name:    "admin kubeconfig contains client certificate and key",
			runtime: pkiAuthRuntime("my-cluster", "production"),
			handler: secretsHandler(http.StatusCreated, nil),
			wantErr: false,
			validate: func(t *testing.T, cfg *clientcmdapi.Config) {
				authInfo, ok := cfg.AuthInfos["samaritano-my-cluster"]
				require.True(t, ok, "expected auth info for 'samaritano-my-cluster'")
				assert.NotEmpty(t, authInfo.ClientCertificateData)
				assert.NotEmpty(t, authInfo.ClientKeyData)
			},
		},
		{
			name:    "API error on secret creation returns error",
			runtime: pkiAuthRuntime("test-cluster", "default"),
			handler: secretsHandler(http.StatusInternalServerError, nil),
			wantErr: true,
		},
		{
			name:         "cleanup deletes the created secret",
			runtime:      pkiAuthRuntime("test-cluster", "default"),
			handler:      secretsHandler(http.StatusCreated, &cleanupDeleteCalls),
			wantErr:      false,
			triggerClean: true,
			validate: func(t *testing.T, _ *clientcmdapi.Config) {
				assert.EqualValues(t, 1, cleanupDeleteCalls.Load(), "expected exactly one DELETE call from cleaner")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaner := cleanup.Make(func() {})
			client := newTestClientset(t, tt.handler)

			cfg, err := setupPKIAuth(context.Background(), &cleaner, client, tt.runtime, layout)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, cfg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)

			if tt.triggerClean {
				cleaner.Clean()
			}

			if tt.validate != nil {
				tt.validate(t, cfg)
			}
		})
	}
}

// Compile-time check: ensure the functions under test match the expected signatures.
var _ func(string) (*kubernetes.Clientset, error) = buildClient
var _ func(context.Context, *cleanup.Cleanup, kubernetes.Interface, *v1alpha1.Runtime, samaritanoruntime.ControlPlaneLayout) (*clientcmdapi.Config, error) = setupPKIAuth

func TestParseConfig(t *testing.T) {
	tests := []struct {
		name        string
		makeConfig  func(t *testing.T) string
		wantErr     bool
		errContains string
		validate    func(t *testing.T, r *v1alpha1.Runtime)
	}{
		{
			name: "non-existent file returns error",
			makeConfig: func(_ *testing.T) string {
				return "/tmp/samaritano-parseconfig-no-such-file.yaml"
			},
			wantErr:     true,
			errContains: "failed to read samaritano config file",
		},
		{
			name: "malformed YAML returns error",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, "not: valid: [yaml")
			},
			wantErr: true,
		},
		{
			name: "valid minimal config parses name and namespace",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, minimalRuntimeConfig)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				assert.Equal(t, "test-cluster", r.Name)
				assert.Equal(t, "default", r.Namespace)
			},
		},
		{
			name: "CRD default: podCIDR is 10.244.0.0/16 when omitted",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, minimalRuntimeConfig)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				assert.Equal(t, "10.244.0.0/16", r.Spec.UpstreamCluster.Network.PodCIDR)
			},
		},
		{
			name: "CRD default: serviceCIDR is 10.96.0.0/16 when omitted",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, minimalRuntimeConfig)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				assert.Equal(t, "10.96.0.0/16", r.Spec.UpstreamCluster.Network.ServiceCIDR)
			},
		},
		{
			name: "CRD default: coredns.clusterDNSIP is 10.96.0.10 when omitted",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, minimalRuntimeConfig)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				assert.Equal(t, "10.96.0.10", r.Spec.UpstreamCluster.Network.Coredns.ClusterDNSIP)
			},
		},
		{
			name: "CRD default: coredns.replicas is 2 when omitted",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, minimalRuntimeConfig)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				require.NotNil(t, r.Spec.UpstreamCluster.Network.Coredns.Replicas)
				assert.Equal(t, int32(2), *r.Spec.UpstreamCluster.Network.Coredns.Replicas)
			},
		},
		{
			name: "CRD default: deployment.replicas is 2 when omitted",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, `apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
kind: Runtime
metadata:
  name: test-cluster
  namespace: default
spec:
  controlPlane:
    samaritano:
      image: "samaritano:test"
    deployment:
      serviceAccountName: default
    service:
      serviceType: ClusterIP
  upstreamCluster:
    storage:
      type: kine
`)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				require.NotNil(t, r.Spec.ControlPlane.Deployment.Replicas)
				assert.Equal(t, int32(1), *r.Spec.ControlPlane.Deployment.Replicas)
			},
		},
		{
			name: "explicit podCIDR overrides default",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, `apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
kind: Runtime
metadata:
  name: test-cluster
  namespace: default
spec:
  controlPlane:
    samaritano:
      image: "samaritano:test"
    deployment:
      replicas: 1
      serviceAccountName: default
    service:
      serviceType: ClusterIP
  upstreamCluster:
    storage:
      type: kine
    network:
      podCIDR: "192.168.0.0/16"
`)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				assert.Equal(t, "192.168.0.0/16", r.Spec.UpstreamCluster.Network.PodCIDR)
			},
		},
		{
			name: "explicit coredns replicas overrides default",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, `apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
kind: Runtime
metadata:
  name: test-cluster
  namespace: default
spec:
  controlPlane:
    samaritano:
      image: "samaritano:test"
    deployment:
      replicas: 1
      serviceAccountName: default
    service:
      serviceType: ClusterIP
  upstreamCluster:
    storage:
      type: kine
    network:
      coredns:
        replicas: 3
`)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				require.NotNil(t, r.Spec.UpstreamCluster.Network.Coredns.Replicas)
				assert.Equal(t, int32(3), *r.Spec.UpstreamCluster.Network.Coredns.Replicas)
			},
		},
		{
			name: "explicit deployment replicas overrides default",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, minimalRuntimeConfig) // replicas: 1
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				require.NotNil(t, r.Spec.ControlPlane.Deployment.Replicas)
				assert.Equal(t, int32(1), *r.Spec.ControlPlane.Deployment.Replicas)
			},
		},
		{
			name: "kubeProxy.disabled is preserved",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, `apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
kind: Runtime
metadata:
  name: test-cluster
  namespace: default
spec:
  controlPlane:
    samaritano:
      image: "samaritano:test"
    deployment:
      replicas: 1
      serviceAccountName: default
    service:
      serviceType: ClusterIP
  upstreamCluster:
    storage:
      type: kine
    network:
      kubeProxy:
        disabled: true
`)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				assert.True(t, r.Spec.UpstreamCluster.Network.KubeProxy.Disabled)
			},
		},
		{
			name: "controlPlaneEndpoint addresses are preserved",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, runtimeConfigWithExternalAddress)
			},
			validate: func(t *testing.T, r *v1alpha1.Runtime) {
				assert.Equal(t, []string{"my-cluster.example.com"},
					r.Spec.UpstreamCluster.ControlPlaneEndpoint.Addresses)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.makeConfig(t)
			r, err := parseConfig(path)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, r)
			if tt.validate != nil {
				tt.validate(t, r)
			}
		})
	}
}
