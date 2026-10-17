package controlplane

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// sampleKubeconfig builds a minimal, realistic kubeconfig matching the shape
// pkg/runtime.generateKubeconfig produces: one cluster, one user, one context, all keyed by name.
func sampleKubeconfig(t *testing.T, name, server string) []byte {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[name] = &clientcmdapi.Cluster{
		Server:                   server,
		CertificateAuthorityData: []byte("fake-ca-data"),
	}
	cfg.AuthInfos[name] = &clientcmdapi.AuthInfo{
		ClientCertificateData: []byte("fake-cert-data"),
		ClientKeyData:         []byte("fake-key-data"),
	}
	contextName := name + "@heir"
	cfg.Contexts[contextName] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	cfg.CurrentContext = contextName

	data, err := clientcmd.Write(*cfg)
	require.NoError(t, err)
	return data
}

func TestFetchKubeconfig(t *testing.T) {
	const (
		name = "my-cluster"
		ns   = "default"
	)

	tests := []struct {
		name         string
		seedSecret   *corev1.Secret
		server       string
		wantErr      string
		validateData func(t *testing.T, data []byte)
	}{
		{
			name: "returns the kubeconfig bytes as-is when no --server override is given",
			seedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-kubeconfig", Namespace: ns},
				Data:       map[string][]byte{"kubeconfig": sampleKubeconfig(t, name, "https://10.0.0.1:6443")},
			},
			validateData: func(t *testing.T, data []byte) {
				cfg, err := clientcmd.Load(data)
				require.NoError(t, err)
				require.Contains(t, cfg.Clusters, name)
				assert.Equal(t, "https://10.0.0.1:6443", cfg.Clusters[name].Server)
			},
		},
		{
			name: "--server rewrites the cluster's server URL without touching other fields",
			seedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-kubeconfig", Namespace: ns},
				Data:       map[string][]byte{"kubeconfig": sampleKubeconfig(t, name, "https://10.0.0.1:6443")},
			},
			server: "https://127.0.0.1:30080",
			validateData: func(t *testing.T, data []byte) {
				cfg, err := clientcmd.Load(data)
				require.NoError(t, err)
				require.Contains(t, cfg.Clusters, name)
				assert.Equal(t, "https://127.0.0.1:30080", cfg.Clusters[name].Server)
				assert.Equal(t, []byte("fake-ca-data"), cfg.Clusters[name].CertificateAuthorityData)
				require.Contains(t, cfg.AuthInfos, name)
				assert.Equal(t, []byte("fake-cert-data"), cfg.AuthInfos[name].ClientCertificateData)
				assert.Equal(t, name+"@heir", cfg.CurrentContext)
			},
		},
		{
			name: "missing secret produces a clear, actionable error listing possible causes",
			wantErr: `failed to generate cluster kubeconfig: cluster "my-cluster" was not found, this might be because:
  (1) cluster provisioning is not fully complete yet — wait a bit and try again
  (2) your control plane was created in a different namespace (use --namespace to switch)
  (3) you're targeting the wrong management cluster (use --kubeconfig to point at the right one)`,
		},
		{
			name: "secret without a kubeconfig key produces a clear error",
			seedSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-kubeconfig", Namespace: ns},
				Data:       map[string][]byte{"not-kubeconfig": []byte("irrelevant")},
			},
			wantErr: `secret default/my-cluster-kubeconfig has no "kubeconfig" key`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.seedSecret != nil {
				builder = builder.WithObjects(tt.seedSecret)
			}
			fakeClient := builder.Build()

			data, err := FetchKubeconfig(context.Background(),
				WithName(name),
				WithNamespace(ns),
				WithServer(tt.server),
				withControllerClient(fakeClient),
			)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}
			require.NoError(t, err)
			tt.validateData(t, data)
		})
	}
}

func TestRewriteServer(t *testing.T) {
	t.Run("rewrites every cluster entry", func(t *testing.T) {
		original := sampleKubeconfig(t, "my-cluster", "https://old:6443")
		out, err := rewriteServer(original, "https://new:30080")
		require.NoError(t, err)

		cfg, err := clientcmd.Load(out)
		require.NoError(t, err)
		require.Contains(t, cfg.Clusters, "my-cluster")
		assert.Equal(t, "https://new:30080", cfg.Clusters["my-cluster"].Server)
	})

	t.Run("malformed input returns an error", func(t *testing.T) {
		_, err := rewriteServer([]byte("not: valid: [kubeconfig"), "https://new:30080")
		assert.Error(t, err)
	})
}
