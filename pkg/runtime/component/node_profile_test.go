package component

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"

	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
)

func defaultNodeProfileRuntime(kubelet controlplanev1alpha1.KubeletSpec) *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		Spec: controlplanev1alpha1.RuntimeSpec{
			UpstreamCluster: controlplanev1alpha1.UpstreamCluster{
				Network: controlplanev1alpha1.NetworkSpec{
					Coredns: controlplanev1alpha1.CorednsSpec{
						ClusterDNSIP: "10.96.0.10",
					},
					CNI: controlplanev1alpha1.CNISpec{
						Supplier: "flannel",
					},
				},
				Kubelet: kubelet,
			},
		},
	}
}

func defaultNodeProfileWrkCtx() *typ.WorkerContext {
	return typ.NewWorkerContextWithDefaults()
}

func parseConfigMap(t *testing.T, manifest []byte) corev1.ConfigMap {
	t.Helper()
	var cm corev1.ConfigMap
	require.NoError(t, sigsyaml.Unmarshal(manifest, &cm))
	return cm
}

func TestCreateNodeProfileManifest(t *testing.T) {
	tests := []struct {
		name     string
		wrkCtx   *typ.WorkerContext
		runtime  *controlplanev1alpha1.Runtime
		wantErr  bool
		validate func(t *testing.T, cm corev1.ConfigMap)
	}{
		{
			name:    "produces a ConfigMap in kube-system with the default name",
			wrkCtx:  defaultNodeProfileWrkCtx(),
			runtime: defaultNodeProfileRuntime(controlplanev1alpha1.KubeletSpec{}),
			validate: func(t *testing.T, cm corev1.ConfigMap) {
				assert.Equal(t, "v1", cm.APIVersion)
				assert.Equal(t, "ConfigMap", cm.Kind)
				assert.Equal(t, "kube-system", cm.Namespace)
				assert.Equal(t, "worker-profile", cm.Name)
			},
		},
		{
			name:    "both kubelet.configuration and kubelet.extraArgs keys are present",
			wrkCtx:  defaultNodeProfileWrkCtx(),
			runtime: defaultNodeProfileRuntime(controlplanev1alpha1.KubeletSpec{}),
			validate: func(t *testing.T, cm corev1.ConfigMap) {
				raw, ok := cm.Data["kubelet.configuration"]
				require.True(t, ok, "expected kubelet.configuration key in ConfigMap data")
				var kubeletCfg map[string]interface{}
				require.NoError(t, sigsyaml.Unmarshal([]byte(raw), &kubeletCfg))

				_, ok = cm.Data["kubelet.extraArgs"]
				assert.True(t, ok, "expected kubelet.extraArgs key in ConfigMap data")

				_, ok = cm.Data["cni.provider"]
				assert.True(t, ok, "expected cni.provider key in ConfigMap data")
			},
		},
		{
			name:    "kubelet configuration reflects WorkerContext defaults",
			wrkCtx:  defaultNodeProfileWrkCtx(),
			runtime: defaultNodeProfileRuntime(controlplanev1alpha1.KubeletSpec{}),
			validate: func(t *testing.T, cm corev1.ConfigMap) {
				var kubeletCfg map[string]interface{}
				require.NoError(t, sigsyaml.Unmarshal([]byte(cm.Data["kubelet.configuration"]), &kubeletCfg))

				auth, _ := kubeletCfg["authentication"].(map[string]interface{})
				x509, _ := auth["x509"].(map[string]interface{})
				assert.Equal(t, "/etc/samaritano/pki/ca.crt", x509["clientCAFile"])
				assert.Equal(t, "/run/samaritano/containerd.sock", kubeletCfg["containerRuntimeEndpoint"])
				assert.Equal(t, "/etc/samaritano/manifests", kubeletCfg["staticPodPath"])

				dns, _ := kubeletCfg["clusterDNS"].([]interface{})
				require.Len(t, dns, 1)
				assert.Equal(t, "10.96.0.10", dns[0])
			},
		},
		{
			name:   "config patch overrides a kubelet configuration field",
			wrkCtx: defaultNodeProfileWrkCtx(),
			runtime: defaultNodeProfileRuntime(controlplanev1alpha1.KubeletSpec{
				ConfigPatches: "cgroupDriver: cgroupfs\nfailSwapOn: true\n",
			}),
			validate: func(t *testing.T, cm corev1.ConfigMap) {
				var kubeletCfg map[string]interface{}
				require.NoError(t, sigsyaml.Unmarshal([]byte(cm.Data["kubelet.configuration"]), &kubeletCfg))
				assert.Equal(t, "cgroupfs", kubeletCfg["cgroupDriver"])
				assert.Equal(t, true, kubeletCfg["failSwapOn"])
			},
		},
		{
			name:   "config patch does not drop unpatched fields",
			wrkCtx: defaultNodeProfileWrkCtx(),
			runtime: defaultNodeProfileRuntime(controlplanev1alpha1.KubeletSpec{
				ConfigPatches: "cgroupDriver: cgroupfs\n",
			}),
			validate: func(t *testing.T, cm corev1.ConfigMap) {
				var kubeletCfg map[string]interface{}
				require.NoError(t, sigsyaml.Unmarshal([]byte(cm.Data["kubelet.configuration"]), &kubeletCfg))
				assert.Equal(t, "cgroupfs", kubeletCfg["cgroupDriver"])
				// staticPodPath and containerRuntimeEndpoint must still be present
				assert.Equal(t, "/etc/samaritano/manifests", kubeletCfg["staticPodPath"])
				assert.Equal(t, "/run/samaritano/containerd.sock", kubeletCfg["containerRuntimeEndpoint"])
			},
		},
		{
			name:   "extra args are stored as YAML under kubelet.extraArgs key",
			wrkCtx: defaultNodeProfileWrkCtx(),
			runtime: defaultNodeProfileRuntime(controlplanev1alpha1.KubeletSpec{
				ExtraArgs: map[string]string{
					"v":           "4",
					"node-labels": "env=prod",
					"max-pods":    "110",
				},
			}),
			validate: func(t *testing.T, cm corev1.ConfigMap) {
				raw, ok := cm.Data["kubelet.extraArgs"]
				require.True(t, ok, "expected kubelet.extraArgs key in ConfigMap data")
				var extraArgs map[string]string
				require.NoError(t, sigsyaml.Unmarshal([]byte(raw), &extraArgs))
				assert.Equal(t, "4", extraArgs["v"])
				assert.Equal(t, "env=prod", extraArgs["node-labels"])
				assert.Equal(t, "110", extraArgs["max-pods"])
			},
		},
		{
			name:    "kubelet.extraArgs key is present and empty when no extra args are set",
			wrkCtx:  defaultNodeProfileWrkCtx(),
			runtime: defaultNodeProfileRuntime(controlplanev1alpha1.KubeletSpec{}),
			validate: func(t *testing.T, cm corev1.ConfigMap) {
				raw, ok := cm.Data["kubelet.extraArgs"]
				require.True(t, ok, "expected kubelet.extraArgs key even when no args are set")

				var extraArgs map[string]string
				require.NoError(t, sigsyaml.Unmarshal([]byte(raw), &extraArgs))
				assert.Empty(t, extraArgs)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest, err := CreateNodeProfileManifest(tt.wrkCtx, tt.runtime)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, manifest)

			cm := parseConfigMap(t, manifest)
			tt.validate(t, cm)
		})
	}
}
