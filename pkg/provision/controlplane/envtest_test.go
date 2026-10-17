package controlplane

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// envtestClient talks to the real API server TestMain starts below. It's only used by tests
// that need genuine CRD structural defaulting, which the fake client in provision_test.go
// deliberately does not perform (see sigs.k8s.io/controller-runtime/pkg/client/fake's own
// doc.go: "No OpenAPI validation is performed when creating or updating objects.").
var envtestClient client.Client

func TestMain(m *testing.M) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "controlplane", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if dir := firstEnvtestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start envtest environment: %v\n", err)
		os.Exit(1)
	}

	scheme := k8sruntime.NewScheme()
	if err := controlplanev1alpha1.AddToScheme(scheme); err != nil {
		fmt.Fprintf(os.Stderr, "failed to register controlplane scheme: %v\n", err)
		os.Exit(1)
	}
	envtestClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build envtest client: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := testEnv.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to stop envtest environment: %v\n", err)
	}
	os.Exit(code)
}

// firstEnvtestBinaryDir mirrors getFirstFoundEnvTestBinaryDir in
// internal/controller/controlplane/suite_test.go, so running `go test` directly (e.g. from an
// IDE) after `make setup-envtest` finds the downloaded binaries without KUBEBUILDER_ASSETS set.
func firstEnvtestBinaryDir() string {
	basePath := filepath.Join("..", "..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

// TestControlPlaneExternalEndpointRequired asserts that cluster.controlPlaneExternalEndpoint's
// apiServer/planeTunnel ports have no default and must be set explicitly: creating a Runtime
// that omits them fails clearly, instead of silently persisting an unreachable port 0.
func TestControlPlaneExternalEndpointRequired(t *testing.T) {
	rt := &controlplanev1alpha1.Runtime{
		ObjectMeta: metav1.ObjectMeta{Name: "endpoint-required-test", Namespace: "default"},
	}
	err := envtestClient.Create(context.Background(), rt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controlPlaneExternalEndpoint.apiServer.port")
	assert.Contains(t, err.Error(), "controlPlaneExternalEndpoint.planeTunnel.port")
}

// TestRuntimeDefaults asserts that creating a Runtime (with only the now-required endpoint
// ports set) against a real API server picks up the CRD's own +kubebuilder:default values for
// everything else — the mechanism Provision's doc comment relies on instead of hand-building a
// default spec in Go.
func TestRuntimeDefaults(t *testing.T) {
	rt := &controlplanev1alpha1.Runtime{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "defaults-test",
			Namespace: "default",
		},
		Spec: controlplanev1alpha1.RuntimeSpec{
			Cluster: controlplanev1alpha1.ClusterSpec{
				ControlPlaneExternalEndpoint: controlplanev1alpha1.ControlPlaneExternalEndpointSpec{
					APIServer:   controlplanev1alpha1.ComponentEndpoint{Port: 30080},
					PlaneTunnel: controlplanev1alpha1.ComponentEndpoint{Port: 30081},
				},
			},
		},
	}
	require.NoError(t, envtestClient.Create(context.Background(), rt))
	t.Cleanup(func() {
		_ = envtestClient.Delete(context.Background(), rt)
	})

	var got controlplanev1alpha1.Runtime
	require.NoError(t, envtestClient.Get(context.Background(), client.ObjectKeyFromObject(rt), &got))

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"cluster.storage.type", got.Spec.Cluster.Storage.Type, "kine"},
		{"cluster.network.podCIDR", got.Spec.Cluster.Network.PodCIDR, "10.244.0.0/16"},
		{"cluster.network.serviceCIDR", got.Spec.Cluster.Network.ServiceCIDR, "10.96.0.0/16"},
		{"cluster.network.dnsDomain", got.Spec.Cluster.Network.DNSDomain, "cluster.local"},
		{
			"controlPlane.planeTunnel.server.image",
			got.Spec.ControlPlane.PlaneTunnel.Server.Image,
			"ghcr.io/tardigradeproj/heir-tunnel:latest",
		},
		{"controlPlane.deployment.serviceAccountName", got.Spec.ControlPlane.Deployment.ServiceAccountName, "default"},
		{"controlPlane.service.serviceType", got.Spec.ControlPlane.Service.ServiceType, corev1.ServiceType("NodePort")},
		{"controlPlane.service.apiServerNodePort", got.Spec.ControlPlane.Service.ApiServerNodePort, ptr.To(int32(30080))},
		{"controlPlane.heir.image", got.Spec.ControlPlane.Heir.Image, "ghcr.io/tardigradeproj/heir:latest"},
		{"controlPlane.clusterAgent.image", got.Spec.ControlPlane.ClusterAgent.Image, "ghcr.io/tardigradeproj/clusteragent:latest"},
		{"controlPlane.planeTunnel.service.serviceType", got.Spec.ControlPlane.PlaneTunnel.Service.ServiceType, corev1.ServiceType("NodePort")},
		{"controlPlane.planeTunnel.service.nodePort", got.Spec.ControlPlane.PlaneTunnel.Service.NodePort, ptr.To(int32(30081))},
		// cluster.controlPlaneExternalEndpoint.apiServer.port and .planeTunnel.port are NOT
		// asserted here: their defaults ({port:30080}/{port:30081}) are declared on the parent
		// ComponentEndpoint field as whole-object defaults, which Kubernetes only substitutes
		// when that entire key is absent. Since ComponentEndpoint is a plain (non-pointer)
		// struct, it's always present once this Go client marshals it — and Port itself has no
		// default of its own — so these come back 0 for any Go client, confirmed empirically.
		// Fixing it would mean splitting ComponentEndpoint into two single-purpose types (one
		// per default), a larger change than this test is about.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}

	// Replicas is a *int32, so it gets its own assertion rather than sitting in the table above.
	if assert.NotNil(t, got.Spec.ControlPlane.Deployment.Replicas, "controlPlane.deployment.replicas should default to a non-nil pointer") {
		assert.Equal(t, int32(1), *got.Spec.ControlPlane.Deployment.Replicas)
	}
}
