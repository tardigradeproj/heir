package component

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	sigsyaml "sigs.k8s.io/yaml"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
)

// parseManifest splits a multi-document YAML manifest and returns a map of "Kind/name" -> raw JSON bytes.
func parseManifest(t *testing.T, manifest []byte) map[string][]byte {
	t.Helper()
	resources := make(map[string][]byte)
	decoder := yaml.NewYAMLToJSONDecoder(bytes.NewReader(manifest))
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			break
		}
		if len(raw) == 0 {
			continue
		}
		var meta struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}
		require.NoError(t, json.Unmarshal(raw, &meta))
		key := meta.Kind + "/" + meta.Metadata.Name
		resources[key] = raw
	}
	return resources
}

func kubeProxyRuntime(spec controlplanev1alpha1.KubeProxySpec) *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		Spec: controlplanev1alpha1.RuntimeSpec{
			Cluster: controlplanev1alpha1.ClusterSpec{
				Network: controlplanev1alpha1.NetworkSpec{
					PodCIDR:   "10.244.0.0/16",
					KubeProxy: spec,
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
		RegistrySettings: controlplanev1alpha1.RegistrySettings{
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
	assert.Equal(t, "https://127.0.0.1:6443", cfg.ControlPlaneEndpoint)
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

func TestCreateManifest(t *testing.T) {
	tests := []struct {
		name     string
		spec     controlplanev1alpha1.KubeProxySpec
		wantErr  bool
		validate func(t *testing.T, resources map[string][]byte)
	}{
		{
			name:    "disabled returns error due to nil template data",
			spec:    controlplanev1alpha1.KubeProxySpec{Disabled: true},
			wantErr: false,
			validate: func(t *testing.T, resources map[string][]byte) {
				assert.Equal(t, resources, map[string][]byte{})
			},
		},
		{
			name: "all expected resource kinds are present",
			spec: controlplanev1alpha1.KubeProxySpec{
				RegistrySettings: controlplanev1alpha1.RegistrySettings{
					Registry: "registry.k8s.io",
					Image:    "kube-proxy:v1.34.0",
				},
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				for _, key := range []string{
					"ServiceAccount/kube-proxy",
					"Role/kube-proxy",
					"ClusterRoleBinding/node-proxier",
					"RoleBinding/kube-proxy",
					"ConfigMap/kube-proxy",
					"DaemonSet/kube-proxy",
				} {
					assert.Contains(t, resources, key, "missing resource %s", key)
				}
			},
		},
		{
			name: "service account is in kube-system namespace",
			spec: controlplanev1alpha1.KubeProxySpec{},
			validate: func(t *testing.T, resources map[string][]byte) {
				var sa corev1.ServiceAccount
				require.NoError(t, sigsyaml.Unmarshal(resources["ServiceAccount/kube-proxy"], &sa))
				assert.Equal(t, "kube-proxy", sa.Name)
				assert.Equal(t, "kube-system", sa.Namespace)
			},
		},
		{
			name: "cluster role binding references system:node-proxier",
			spec: controlplanev1alpha1.KubeProxySpec{},
			validate: func(t *testing.T, resources map[string][]byte) {
				var crb rbacv1.ClusterRoleBinding
				require.NoError(t, sigsyaml.Unmarshal(resources["ClusterRoleBinding/node-proxier"], &crb))
				assert.Equal(t, "system:node-proxier", crb.RoleRef.Name)
				assert.Equal(t, "kube-proxy", crb.Subjects[0].Name)
				assert.Equal(t, "kube-system", crb.Subjects[0].Namespace)
			},
		},
		{
			name: "configmap contains cluster CIDR and control plane endpoint",
			spec: controlplanev1alpha1.KubeProxySpec{},
			validate: func(t *testing.T, resources map[string][]byte) {
				var cm corev1.ConfigMap
				require.NoError(t, sigsyaml.Unmarshal(resources["ConfigMap/kube-proxy"], &cm))
				assert.Contains(t, cm.Data["config.conf"], "clusterCIDR: 10.244.0.0/16")
				assert.Contains(t, cm.Data["kubeconfig.conf"], "server: https://127.0.0.1:6443")
			},
		},
		{
			name: "configmap reflects proxy mode",
			spec: controlplanev1alpha1.KubeProxySpec{Mode: "ipvs"},
			validate: func(t *testing.T, resources map[string][]byte) {
				var cm corev1.ConfigMap
				require.NoError(t, sigsyaml.Unmarshal(resources["ConfigMap/kube-proxy"], &cm))
				assert.Contains(t, cm.Data["config.conf"], `mode: "ipvs"`)
			},
		},
		{
			name: "daemonset uses image from registry settings",
			spec: controlplanev1alpha1.KubeProxySpec{
				RegistrySettings: controlplanev1alpha1.RegistrySettings{
					Registry:   "my.registry.io",
					Image:      "kube-proxy:v1.30.0",
					PullPolicy: corev1.PullAlways,
				},
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				var ds appsv1.DaemonSet
				require.NoError(t, sigsyaml.Unmarshal(resources["DaemonSet/kube-proxy"], &ds))
				container := ds.Spec.Template.Spec.Containers[0]
				assert.Equal(t, "my.registry.io/kube-proxy:v1.30.0", container.Image)
				assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
			},
		},
		{
			name: "daemonset args include extra args",
			spec: controlplanev1alpha1.KubeProxySpec{
				ExtraArgs: map[string]string{"v": "4"},
			},
			validate: func(t *testing.T, resources map[string][]byte) {
				var ds appsv1.DaemonSet
				require.NoError(t, sigsyaml.Unmarshal(resources["DaemonSet/kube-proxy"], &ds))
				assert.Contains(t, ds.Spec.Template.Spec.Containers[0].Args, "--v=4")
			},
		},
		{
			name: "daemonset mounts NODE_NAME env from field ref",
			spec: controlplanev1alpha1.KubeProxySpec{},
			validate: func(t *testing.T, resources map[string][]byte) {
				var ds appsv1.DaemonSet
				require.NoError(t, sigsyaml.Unmarshal(resources["DaemonSet/kube-proxy"], &ds))
				envVars := ds.Spec.Template.Spec.Containers[0].Env
				require.Len(t, envVars, 1)
				assert.Equal(t, "NODE_NAME", envVars[0].Name)
				assert.Equal(t, "spec.nodeName", envVars[0].ValueFrom.FieldRef.FieldPath)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := kubeProxyRuntime(tt.spec)

			manifest, err := CreateKubeProxyManifest(runtime)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			resources := parseManifest(t, manifest)
			tt.validate(t, resources)
		})
	}
}
