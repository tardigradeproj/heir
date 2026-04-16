package component

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	controlplanev1alpha1 "github.com/tardigrade-runtime/samaritano/api/v1alpha1"
)

func kubeProxyRuntime(spec controlplanev1alpha1.KubeProxySpec) *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		Spec: controlplanev1alpha1.RuntimeSpec{
			UpstreamCluster: controlplanev1alpha1.UpstreamCluster{
				APIServer: controlplanev1alpha1.APIServerSpec{
					ExternalAddress: "https://api.example.com:6443",
				},
				Network: controlplanev1alpha1.NetworkSpec{
					PodCIDR:   "10.244.0.0/16",
					KubeProxy: &spec,
				},
			},
		},
	}
}

func TestGetConfig_Disabled(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{Disabled: true})

	cfg, err := getConfig(runtime)
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestGetConfig_BasicFields(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{
		RegisterSetting: controlplanev1alpha1.RegistrySettings{
			Registry:   "registry.k8s.io",
			Image:      "kube-proxy:v1.34.0",
			PullPolicy: corev1.PullIfNotPresent,
		},
		Mode:               "iptables",
		MetricsBindAddress: "0.0.0.0:10249",
	})

	cfg, err := getConfig(runtime)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.True(t, cfg.Enabled)
	assert.False(t, cfg.DualStack)
	assert.Equal(t, "10.244.0.0/16", cfg.ClusterCIDR)
	assert.Equal(t, "https://api.example.com:6443", cfg.ControlPlaneEndpoint)
	assert.Equal(t, "registry.k8s.io/kube-proxy:v1.34.0", cfg.Image)
	assert.Equal(t, string(corev1.PullIfNotPresent), cfg.PullPolicy)
	assert.Equal(t, "iptables", cfg.Mode)
	assert.Equal(t, "0.0.0.0:10249", cfg.MetricsBindAddress)
}

func TestGetConfig_DefaultArgs(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{})

	cfg, err := getConfig(runtime)
	require.NoError(t, err)

	assert.Contains(t, cfg.Args, "--config=/var/lib/kube-proxy/config.conf")
	assert.Contains(t, cfg.Args, "--hostname-override=$(NODE_NAME)")
}

func TestGetConfig_ExtraArgs(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{
		ExtraArgs: map[string]string{
			"v":          "4",
			"kubeconfig": "/custom/path",
		},
	})

	cfg, err := getConfig(runtime)
	require.NoError(t, err)

	assert.Contains(t, cfg.Args, "--v=4")
	assert.Contains(t, cfg.Args, "--kubeconfig=/custom/path")
}

func TestGetConfig_ExtraArgs_OverrideDefault(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{
		ExtraArgs: map[string]string{
			"config": "/custom/config.conf",
		},
	})

	cfg, err := getConfig(runtime)
	require.NoError(t, err)

	assert.Contains(t, cfg.Args, "--config=/custom/config.conf")
	assert.NotContains(t, cfg.Args, "--config=/var/lib/kube-proxy/config.conf")
}

func TestGetConfig_NodePortAddresses(t *testing.T) {
	addrs := []string{"192.168.0.0/24", "10.0.0.0/8"}
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{
		NodePortAddresses: addrs,
	})

	cfg, err := getConfig(runtime)
	require.NoError(t, err)

	want, _ := json.Marshal(addrs)
	assert.Equal(t, string(want), cfg.NodePortAddresses)
}

func TestGetConfig_JSONFields(t *testing.T) {
	runtime := kubeProxyRuntime(controlplanev1alpha1.KubeProxySpec{})

	cfg, err := getConfig(runtime)
	require.NoError(t, err)

	for name, val := range map[string]string{
		"IPTables":          cfg.IPTables,
		"IPVS":              cfg.IPVS,
		"NFTables":          cfg.NFTables,
		"NodePortAddresses": cfg.NodePortAddresses,
	} {
		assert.Truef(t, json.Valid([]byte(val)), "%s is not valid JSON: %q", name, val)
	}
}
