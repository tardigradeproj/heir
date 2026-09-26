package component

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
)

func addonsRuntime(podCIDR string, addons controlplanev1alpha1.AddonsSpec) *controlplanev1alpha1.Runtime {
	return &controlplanev1alpha1.Runtime{
		Spec: controlplanev1alpha1.RuntimeSpec{
			Cluster: controlplanev1alpha1.ClusterSpec{
				Network: controlplanev1alpha1.NetworkSpec{
					PodCIDR: podCIDR,
				},
				Addons: addons,
			},
		},
	}
}

func findChart(charts []clusteragentv1alpha1.HelmChart, name string) (clusteragentv1alpha1.HelmChart, bool) {
	for _, c := range charts {
		if c.Name == name {
			return c, true
		}
	}
	return clusteragentv1alpha1.HelmChart{}, false
}

func TestConsolidateAddons(t *testing.T) {
	tests := []struct {
		name     string
		podCIDR  string
		addons   controlplanev1alpha1.AddonsSpec
		validate func(t *testing.T, charts []clusteragentv1alpha1.HelmChart)
	}{
		{
			name:    "no addons declared installs both built-in defaults with default values",
			podCIDR: "10.244.0.0/16",
			addons:  controlplanev1alpha1.AddonsSpec{},
			validate: func(t *testing.T, charts []clusteragentv1alpha1.HelmChart) {
				require.Len(t, charts, 2)

				coredns, ok := findChart(charts, "coredns")
				require.True(t, ok, "expected coredns chart")
				assert.Equal(t, "kube-system", coredns.Namespace)
				assert.Equal(t, defaultCorednsValues, coredns.Spec.Values.Content)

				flannel, ok := findChart(charts, "flannel")
				require.True(t, ok, "expected flannel chart")
				assert.Equal(t, "kube-flannel", flannel.Namespace)
				assert.Contains(t, flannel.Spec.Values.Content, `podCidr: "10.244.0.0/16"`)
			},
		},
		{
			name:    "disabling a built-in addon removes it",
			podCIDR: "10.244.0.0/16",
			addons: controlplanev1alpha1.AddonsSpec{
				Helm: controlplanev1alpha1.HelmAddonsSpec{
					Charts: []controlplanev1alpha1.AddonChartSpec{
						{Name: "coredns", Enabled: ptr.To(false)},
					},
				},
			},
			validate: func(t *testing.T, charts []clusteragentv1alpha1.HelmChart) {
				require.Len(t, charts, 1)
				_, ok := findChart(charts, "coredns")
				assert.False(t, ok, "coredns should have been removed")
				_, ok = findChart(charts, "flannel")
				assert.True(t, ok, "flannel should remain")
			},
		},
		{
			name:    "override replaces a built-in addon's values entirely",
			podCIDR: "10.244.0.0/16",
			addons: controlplanev1alpha1.AddonsSpec{
				Helm: controlplanev1alpha1.HelmAddonsSpec{
					Charts: []controlplanev1alpha1.AddonChartSpec{
						{
							Name: "flannel",
							Override: &controlplanev1alpha1.AddonOverrideSpec{
								Values: clusteragentv1alpha1.ValuesSpec{Content: "custom: true\n"},
							},
						},
					},
				},
			},
			validate: func(t *testing.T, charts []clusteragentv1alpha1.HelmChart) {
				require.Len(t, charts, 2)

				flannel, ok := findChart(charts, "flannel")
				require.True(t, ok)
				assert.Equal(t, "custom: true\n", flannel.Spec.Values.Content)

				coredns, ok := findChart(charts, "coredns")
				require.True(t, ok, "coredns should be unaffected")
				assert.Equal(t, defaultCorednsValues, coredns.Spec.Values.Content)
			},
		},
		{
			name:    "install replaces a built-in addon's chart source entirely",
			podCIDR: "10.244.0.0/16",
			addons: controlplanev1alpha1.AddonsSpec{
				Helm: controlplanev1alpha1.HelmAddonsSpec{
					Charts: []controlplanev1alpha1.AddonChartSpec{
						{
							Name: "coredns",
							Install: &clusteragentv1alpha1.HelmChartSpec{
								Chart: clusteragentv1alpha1.ChartSpec{
									Name:    "coredns-custom",
									Repo:    "https://example.com/charts",
									Version: "9.9.9",
								},
							},
						},
					},
				},
			},
			validate: func(t *testing.T, charts []clusteragentv1alpha1.HelmChart) {
				require.Len(t, charts, 2)

				coredns, ok := findChart(charts, "coredns")
				require.True(t, ok)
				assert.Equal(t, "kube-system", coredns.Namespace)
				assert.Equal(t, "coredns-custom", coredns.Spec.Chart.Name)
				assert.Equal(t, "https://example.com/charts", coredns.Spec.Chart.Repo)
				assert.Equal(t, "9.9.9", coredns.Spec.Chart.Version)

				_, ok = findChart(charts, "flannel")
				assert.True(t, ok, "flannel should be unaffected")
			},
		},
		{
			name:    "a user-declared addon with an install spec is added",
			podCIDR: "10.244.0.0/16",
			addons: controlplanev1alpha1.AddonsSpec{
				Helm: controlplanev1alpha1.HelmAddonsSpec{
					Charts: []controlplanev1alpha1.AddonChartSpec{
						{
							Name: "cilium",
							Install: &clusteragentv1alpha1.HelmChartSpec{
								Chart: clusteragentv1alpha1.ChartSpec{
									Name:    "cilium",
									Repo:    "https://helm.cilium.io/",
									Version: "1.15.0",
								},
							},
						},
					},
				},
			},
			validate: func(t *testing.T, charts []clusteragentv1alpha1.HelmChart) {
				require.Len(t, charts, 3)

				cilium, ok := findChart(charts, "cilium")
				require.True(t, ok, "expected cilium chart")
				assert.Equal(t, "kube-system", cilium.Namespace)
				assert.Equal(t, "https://helm.cilium.io/", cilium.Spec.Chart.Repo)
				assert.Equal(t, "1.15.0", cilium.Spec.Chart.Version)

				_, ok = findChart(charts, "coredns")
				assert.True(t, ok)
				_, ok = findChart(charts, "flannel")
				assert.True(t, ok)
			},
		},
		{
			name:    "a declared addon without an install spec and not a built-in is a no-op",
			podCIDR: "10.244.0.0/16",
			addons: controlplanev1alpha1.AddonsSpec{
				Helm: controlplanev1alpha1.HelmAddonsSpec{
					Charts: []controlplanev1alpha1.AddonChartSpec{
						{Name: "unknown"},
					},
				},
			},
			validate: func(t *testing.T, charts []clusteragentv1alpha1.HelmChart) {
				require.Len(t, charts, 2)
				_, ok := findChart(charts, "unknown")
				assert.False(t, ok, "unknown addon should not produce a chart")
			},
		},
		{
			name:    "global runtime image overrides every chart, built-in and installed alike",
			podCIDR: "10.244.0.0/16",
			addons: controlplanev1alpha1.AddonsSpec{
				Helm: controlplanev1alpha1.HelmAddonsSpec{
					Runtime: controlplanev1alpha1.AddonsRuntimeSpec{Image: "registry.example.com/helmer:v1"},
					Charts: []controlplanev1alpha1.AddonChartSpec{
						{
							Name: "cilium",
							Install: &clusteragentv1alpha1.HelmChartSpec{
								Chart: clusteragentv1alpha1.ChartSpec{
									Name: "cilium",
									Repo: "https://helm.cilium.io/",
								},
							},
						},
					},
				},
			},
			validate: func(t *testing.T, charts []clusteragentv1alpha1.HelmChart) {
				require.Len(t, charts, 3)
				for _, c := range charts {
					assert.Equal(t, "registry.example.com/helmer:v1", c.Spec.Runtime.Image, "chart %s", c.Name)
				}
			},
		},
		{
			name:    "flannel values interpolate the runtime's pod CIDR",
			podCIDR: "192.168.0.0/16",
			addons:  controlplanev1alpha1.AddonsSpec{},
			validate: func(t *testing.T, charts []clusteragentv1alpha1.HelmChart) {
				flannel, ok := findChart(charts, "flannel")
				require.True(t, ok)
				assert.Contains(t, flannel.Spec.Values.Content, `podCidr: "192.168.0.0/16"`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := addonsRuntime(tt.podCIDR, tt.addons)

			charts, err := ConsolidateAddons(runtime)
			require.NoError(t, err)

			tt.validate(t, charts)
		})
	}
}
