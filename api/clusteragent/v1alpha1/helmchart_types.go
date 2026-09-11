package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HelmVersion selects the Helm binary version used to install a chart.
type HelmVersion string

const (
	HelmVersionV2 HelmVersion = "v2"
	HelmVersionV3 HelmVersion = "v3"
)

// ChartSpec identifies the Helm chart to install and where it is installed to.
type ChartSpec struct {
	// Name is the Helm chart name in the repository, or a complete HTTPS URL to a
	// chart archive (.tgz).
	// +optional
	Name string `json:"name,omitempty"`

	// Content is a base64-encoded chart archive (.tgz). Overrides Name when set.
	// +optional
	Content string `json:"content,omitempty"`

	// TargetNamespace is the namespace the Helm chart is installed into.
	// +kubebuilder:default="default"
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// CreateNamespace creates TargetNamespace if it does not already exist.
	// +kubebuilder:default=false
	CreateNamespace bool `json:"createNamespace,omitempty"`

	// Version is the Helm chart version to install, when installing from a repository.
	// +optional
	Version string `json:"version,omitempty"`

	// Repo is the Helm chart repository URL.
	// +optional
	Repo string `json:"repo,omitempty"`
}

// HelmOptions controls how the Helm operation itself is run.
type HelmOptions struct {
	// Version is the Helm version to use.
	// +kubebuilder:default="v3"
	// +kubebuilder:validation:Enum=v2;v3
	Version HelmVersion `json:"version,omitempty"`

	// InsecureSkipTLSVerify skips TLS certificate checks when downloading the chart.
	// +kubebuilder:default=false
	InsecureSkipTLSVerify bool `json:"insecureSkipTLSVerify,omitempty"`

	// BackOffLimit is the number of retries allowed before the job is considered failed.
	// +kubebuilder:default=10
	BackOffLimit *int32 `json:"backOffLimit,omitempty"`

	// Timeout for Helm operations, expressed as a duration string (300s, 10m, 1h, etc).
	// +kubebuilder:default="300s"
	Timeout metav1.Duration `json:"timeout,omitempty"`
}

// RuntimeOptions controls the Job the chart controller creates to run Helm.
type RuntimeOptions struct {
	// Bootstrap marks this chart as required to bootstrap the cluster (coreDNS,
	// kube-proxy, etc).
	// +kubebuilder:default=false
	Bootstrap bool `json:"bootstrap,omitempty"`

	// Image is the image used to run the Helm job
	// +kubebuilder:default="ghcr.io/tardigradeproj/helmer:latest"
	Image string `json:"image,omitempty"`

	// SecurityContext is a custom PodSecurityContext applied to the Helm job pod.
	// +optional
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`
}

// ValuesSpec overrides the chart's default values.
type ValuesSpec struct {
	// Set overrides simple chart values. These take precedence over Content.
	// +optional
	Set map[string]string `json:"set,omitempty"`

	// Content overrides complex chart values via inline YAML content.
	// +optional
	Content string `json:"content,omitempty"`
}

// HelmChartSpec defines the desired state of HelmChart
type HelmChartSpec struct {
	// Chart identifies the Helm chart to install and where it is installed to.
	// +required
	Chart ChartSpec `json:"chart"`

	// Helm controls how the Helm operation itself is run.
	// +optional
	Helm HelmOptions `json:"helm,omitzero"`

	// Runtime controls the Job the chart controller creates to run Helm.
	// +optional
	Runtime RuntimeOptions `json:"runtime,omitzero"`

	// Values overrides the chart's default values.
	// +optional
	Values ValuesSpec `json:"values,omitzero"`
}

// HelmChartStatus defines the observed state of HelmChart.
type HelmChartStatus struct {

	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// HelmChart is the Schema for the helmcharts API
type HelmChart struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of HelmChart
	// +required
	Spec HelmChartSpec `json:"spec"`

	// status defines the observed state of HelmChart
	// +optional
	Status HelmChartStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// HelmChartList contains a list of HelmChart
type HelmChartList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []HelmChart `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HelmChart{}, &HelmChartList{})
}
