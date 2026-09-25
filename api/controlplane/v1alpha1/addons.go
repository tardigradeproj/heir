package v1alpha1

import (
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
)

// AddonSpec configures a single cluster addon, keyed by name
type AddonSpec struct {
	// Enabled disables one of Heir's built-in default addons (coredns, kube-proxy, the
	// configured CNI) when set to false. Ignored for addons introduced via Install.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Install adds an entirely new Helm chart to the bootstrap phase, for an addon that
	// isn't one of Heir's built-in defaults. Mirrors clusteragentv1alpha1.HelmChartSpec
	// exactly (see pkg/runtime/helmchart.go), since the cluster agent turns this directly
	// into a HelmChart resource's spec.
	// +optional
	Install *clusteragentv1alpha1.HelmChartSpec `json:"install,omitempty"`

	// Override customizes one of Heir's built-in default addons' Helm values, without
	// changing which chart or version it installs.
	// +optional
	Override *AddonOverrideSpec `json:"override,omitempty"`
}

type AddonOverrideSpec struct {
	// Values overrides the chart's default values.
	// +optional
	Values clusteragentv1alpha1.ValuesSpec `json:"values,omitzero"`
}
