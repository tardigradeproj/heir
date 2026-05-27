package controlplane

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
)

// withClient is a test-only Option that injects a pre-built client, bypassing buildClient.
func withClient(client kubernetes.Interface) Option {
	return func(p *provisionContext) {
		p.client = client
	}
}

// minimalRuntimeConfig is the smallest valid Runtime manifest accepted by Provision.
// storage.type is set explicitly to avoid a CRD generator bug that emits an invalid
// array default for the storage field.
const minimalRuntimeConfig = `apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
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
    controlPlaneEndpoint:
      addresses:
        - "10.0.2.2"
      apiServer:
        port: 30080
      konnectivity:
        port: 30081
`

// runtimeConfigWithExternalAddress has a controlPlaneEndpoint address set.
const runtimeConfigWithExternalAddress = `apiVersion: controlplane.tardigrade.runtime.io/v1alpha1
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
    controlPlaneEndpoint:
      addresses:
      - "my-cluster.example.com"
      apiServer:
        port: 6443
    storage:
      type: kine
`

// writeTempRuntimeConfig writes a Runtime manifest to a temp file and registers cleanup.
func writeTempRuntimeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "runtime-config-*.yaml")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

// injectCreateFailure prepends a reactor that fails the first create of the named resource.
func injectCreateFailure(fakeClient *fake.Clientset, resource string) {
	fakeClient.Fake.PrependReactor("create", resource,
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, fmt.Errorf("injected: create %s failed", resource)
		},
	)
}

// verbCount returns how many recorded actions match the given verb.
func verbCount(fakeClient *fake.Clientset, verb string) int {
	n := 0
	for _, a := range fakeClient.Actions() {
		if a.GetVerb() == verb {
			n++
		}
	}
	return n
}

func TestProvision(t *testing.T) {
	tests := []struct {
		name                  string
		makeConfig            func(t *testing.T) string // nil → write minimalRuntimeConfig
		makeClient            func() *fake.Clientset    // nil → plain fake.NewClientset()
		extraOpts             []Option
		withClusterKubeconfig bool
		wantErr               bool
		errContains           string
		validate              func(t *testing.T, fc *fake.Clientset, clusterKubeconfigPath string)
	}{
		{
			name: "all resources are created on success",
			validate: func(t *testing.T, fc *fake.Clientset, _ string) {
				// pki-auth secret, storage secret, configmap, service, deployment
				assert.Equal(t, 4, verbCount(fc, "create"),
					"expected 4 create actions; got: %v", fc.Actions())
			},
		},
		{
			name: "no cleanup runs on success",
			validate: func(t *testing.T, fc *fake.Clientset, _ string) {
				assert.Equal(t, 0, verbCount(fc, "delete"),
					"cleaner must be released on success — no deletes expected")
			},
		},
		{
			name: "invalid config path returns error",
			makeConfig: func(_ *testing.T) string {
				return "/tmp/samaritano-provision-test-no-such-file.yaml"
			},
			wantErr:     true,
			errContains: "failed to parse config",
		},
		{
			name: "malformed config content returns error",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, "not: valid: [yaml")
			},
			wantErr:     true,
			errContains: "failed to parse config",
		},
		{
			name:      "WithName overrides name from config file",
			extraOpts: []Option{WithName("overridden-cluster")},
			validate: func(t *testing.T, fc *fake.Clientset, _ string) {
				for _, action := range fc.Actions() {
					if action.GetVerb() == "create" {
						obj := action.(k8stesting.CreateAction).GetObject().(metav1.Object)
						assert.True(t,
							containsPrefix(obj.GetName(), "overridden-cluster"),
							"expected resource name to start with 'overridden-cluster', got %q",
							obj.GetName(),
						)
					}
				}
			},
		},
		{
			name:      "WithNamespace overrides namespace from config file",
			extraOpts: []Option{WithNamespace("custom-ns")},
			validate: func(t *testing.T, fc *fake.Clientset, _ string) {
				for _, action := range fc.Actions() {
					assert.Equal(t, "custom-ns", action.GetNamespace(),
						"expected all actions in namespace custom-ns")
				}
			},
		},
		{
			name:                  "kubeconfig is written to file when WithClusterKubeconfig is set",
			withClusterKubeconfig: true,
			validate: func(t *testing.T, _ *fake.Clientset, clusterKubeconfigPath string) {
				_, err := os.Stat(clusterKubeconfigPath)
				require.NoError(t, err, "kubeconfig file should exist at %s", clusterKubeconfigPath)
				cfg, err := clientcmd.LoadFromFile(clusterKubeconfigPath)
				require.NoError(t, err)
				assert.Equal(t, "samaritano-test-cluster@kubernetes", cfg.CurrentContext)
			},
		},
		{
			name: "kubeconfig server address is set from externalAddress",
			makeConfig: func(t *testing.T) string {
				return writeTempRuntimeConfig(t, runtimeConfigWithExternalAddress)
			},
			withClusterKubeconfig: true,
			validate: func(t *testing.T, _ *fake.Clientset, clusterKubeconfigPath string) {
				cfg, err := clientcmd.LoadFromFile(clusterKubeconfigPath)
				require.NoError(t, err)
				cluster, ok := cfg.Clusters["kubernetes"]
				require.True(t, ok, "expected cluster 'kubernetes' in written kubeconfig")
				assert.Equal(t, "https://my-cluster.example.com:6443", cluster.Server)
			},
		},
		{
			name: "PKI auth failure returns error with no cleanup registered",
			makeClient: func() *fake.Clientset {
				fc := fake.NewClientset()
				injectCreateFailure(fc, "secrets")
				return fc
			},
			wantErr:     true,
			errContains: "failed to setup PKI auth",
			validate: func(t *testing.T, fc *fake.Clientset, _ string) {
				// Create failed before cleaner.Add was reached — nothing to delete.
				assert.Equal(t, 0, verbCount(fc, "delete"))
			},
		},
		{
			name: "deployment failure rolls back all previously created resources",
			makeClient: func() *fake.Clientset {
				fc := fake.NewClientset()
				injectCreateFailure(fc, "deployments")
				return fc
			},
			wantErr:     true,
			errContains: "failed to setup deployment",
			validate: func(t *testing.T, fc *fake.Clientset, _ string) {
				// PKI, storage, config, service all created before deployment failed.
				assert.Equal(t, 3, verbCount(fc, "delete"),
					"expected 3 deletes (pki-auth, storage, configmap, service); got: %v",
					fc.Actions())
			},
		},
		{
			name: "service failure rolls back PKI, storage, and config",
			makeClient: func() *fake.Clientset {
				fc := fake.NewClientset()
				injectCreateFailure(fc, "services")
				return fc
			},
			wantErr:     true,
			errContains: "failed to setup service",
			validate: func(t *testing.T, fc *fake.Clientset, _ string) {
				assert.Equal(t, 2, verbCount(fc, "delete"),
					"expected 2 deletes (pki-auth, storage, configmap); got: %v",
					fc.Actions())
			},
		},
		{
			name: "created secret contains PKI data for the runtime",
			validate: func(t *testing.T, fc *fake.Clientset, _ string) {
				secret, err := fc.CoreV1().Secrets("default").Get(
					context.Background(), "test-cluster-pki-auth", metav1.GetOptions{})
				require.NoError(t, err)
				assert.NotEmpty(t, secret.Data["ca.crt"], "CA cert should be present")
				assert.NotEmpty(t, secret.Data["ca.key"], "CA key should be present")
				assert.NotEmpty(t, secret.Data["admin.conf"], "admin kubeconfig should be present")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fc *fake.Clientset
			if tt.makeClient != nil {
				fc = tt.makeClient()
			} else {
				fc = fake.NewClientset()
			}

			var configPath string
			if tt.makeConfig != nil {
				configPath = tt.makeConfig(t)
			} else {
				configPath = writeTempRuntimeConfig(t, minimalRuntimeConfig)
			}

			clusterKubeconfigPath := filepath.Join(t.TempDir(), "cluster-kubeconfig.yaml")

			opts := []Option{
				WithConfig(configPath),
				withClient(fc),
			}
			opts = append(opts, tt.extraOpts...)
			if tt.withClusterKubeconfig {
				opts = append(opts, WithClusterKubeconfig(clusterKubeconfigPath))
			}

			err := Provision(context.Background(), opts...)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.validate != nil {
				tt.validate(t, fc, clusterKubeconfigPath)
			}
		})
	}
}

// containsPrefix reports whether s starts with prefix.
func containsPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// firstDeleteAction returns the first delete action recorded, or nil.
func firstDeleteAction(fc *fake.Clientset) k8stesting.DeleteAction {
	for _, a := range fc.Actions() {
		if a.GetVerb() == "delete" {
			return a.(k8stesting.DeleteAction)
		}
	}
	return nil
}

// Verify fake.Clientset satisfies kubernetes.Interface (used as withClient argument).
var _ kubernetes.Interface = (*fake.Clientset)(nil)

// TestProvisionCreatesNamespacedResources verifies all resources land in the correct namespace.
func TestProvisionCreatesNamespacedResources(t *testing.T) {
	fc := fake.NewClientset()
	configPath := writeTempRuntimeConfig(t, minimalRuntimeConfig)

	err := Provision(context.Background(), WithConfig(configPath), withClient(fc))
	require.NoError(t, err)

	for _, action := range fc.Actions() {
		if action.GetVerb() == "create" {
			assert.Equal(t, "default", action.GetNamespace(),
				"resource %s/%s created in wrong namespace",
				action.GetResource().Resource, action.GetNamespace())
		}
	}
}
