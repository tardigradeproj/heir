package v1alpha1

type CorednsSpec struct {
	//+kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`
	//+kubebuilder:default={"register":"registry.k8s.io", "image":"coredns/coredns:v1.12.1"}
	RegistrySettings RegistrySettings `json:"registrySettings,omitempty"`
	//+kubebuilder:default="10.96.0.10"
	ClusterDNSIP string `json:"clusterDNSIP,omitempty"`
}
