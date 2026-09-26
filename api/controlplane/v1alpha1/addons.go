package v1alpha1

import (
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
)

// AddonsSpec configures cluster addons.
type AddonsSpec struct {
	// Helm configures cluster addons installed via the HelmChart controller.
	// +kubebuilder:default={}
	Helm HelmAddonsSpec `json:"helm,omitempty"`
}

// HelmAddonsSpec configures Helm-based cluster addons.
type HelmAddonsSpec struct {
	// Runtime holds settings applied to every addon's HelmChart.
	// +kubebuilder:default={}
	Runtime AddonsRuntimeSpec `json:"runtime,omitempty"`

	// Charts configures cluster addons
	// +optional
	// +listType=map
	// +listMapKey=name
	Charts []AddonChartSpec `json:"charts,omitempty"`
}

// AddonsRuntimeSpec holds settings applied to every addon's HelmChart.
type AddonsRuntimeSpec struct {
	// Image overrides the image (HelmChart.Spec.Runtime.Image) that runs `helm` for every
	// addon's installer Job.
	// +kubebuilder:default="ghcr.io/tardigradeproj/helmer:latest"
	Image string `json:"image,omitempty"`
}

// AddonChartSpec configures a single cluster addon, named by Name. Enabled only applies to one
// of Heir's built-in default addons; Install only to a new addon; Override only to customizing
// a default addon's values without changing its chart source.
type AddonChartSpec struct {
	// Name identifies the addon.
	// +required
	Name string `json:"name"`

	// Enabled disables one of Heir's built-in default addons when set to false.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Install adds an entirely new Helm chart for an addon that isn't one of Heir's built-in
	// defaults. Mirrors clusteragentv1alpha1.HelmChartSpec exactly, since the cluster agent
	// turns this directly into a HelmChart resource's spec.
	// +optional
	Install *clusteragentv1alpha1.HelmChartSpec `json:"install,omitempty"`

	// Override customizes one of Heir's built-in default addons' Helm values.
	// +optional
	Override *AddonOverrideSpec `json:"override,omitempty"`
}

type AddonOverrideSpec struct {
	// Values overrides the chart's default values.
	// +optional
	Values clusteragentv1alpha1.ValuesSpec `json:"values,omitzero"`
}
