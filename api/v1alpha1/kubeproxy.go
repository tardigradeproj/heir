package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type KubeProxySpec struct {
	Disabled bool `json:"disabled,omitempty"`
	//+kubebuilder:default={"register":"registry.k8s.io", "image":"kube-proxy:v1.34.0"}
	RegisterSetting RegistrySettings `json:"registerSetting,omitempty"`
	// Mode defines the kube-proxy mode.
	// +kubebuilder:validation:Enum=iptables;ipvs;userspace;nft
	//+kubebuilder:default="iptables"
	Mode               string `json:"mode,omitempty"`
	MetricsBindAddress string `json:"metricsBindAddress,omitempty"`
	// +optional
	IPTables KubeProxyIPTablesConfiguration `json:"iptables"`
	// +optional
	IPVS KubeProxyIPVSConfiguration `json:"ipvs"`
	// +optional
	NFTables          KubeProxyNFTablesConfiguration `json:"nftables"`
	NodePortAddresses []string                       `json:"nodePortAddresses,omitempty"`

	// Map of key-values (strings) for any extra arguments to pass down to kube-proxy process
	// Any behavior triggered by these parameters is outside k0s support.
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}

// KubeProxyIPTablesConfiguration contains iptables-related kube-proxy configuration
// @see https://github.com/kubernetes/kube-proxy/blob/v0.34.3/config/v1alpha1/types.go#L27-L48
type KubeProxyIPTablesConfiguration struct {
	MasqueradeBit      *int32 `json:"masqueradeBit,omitempty"`
	MasqueradeAll      bool   `json:"masqueradeAll,omitempty"`
	LocalhostNodePorts *bool  `json:"localhostNodePorts,omitempty"`
	// +optional
	SyncPeriod metav1.Duration `json:"syncPeriod"`
	// +optional
	MinSyncPeriod metav1.Duration `json:"minSyncPeriod"`
}

// KubeProxyIPVSConfiguration contains ipvs-related kube-proxy configuration
// @see https://github.com/kubernetes/kube-proxy/blob/v0.34.3/config/v1alpha1/types.go#L52-L78
type KubeProxyIPVSConfiguration struct {
	// +optional
	SyncPeriod metav1.Duration `json:"syncPeriod"`
	// +optional
	MinSyncPeriod metav1.Duration `json:"minSyncPeriod"`
	Scheduler     string          `json:"scheduler,omitempty"`
	ExcludeCIDRs  []string        `json:"excludeCIDRs,omitempty"`
	StrictARP     bool            `json:"strictARP,omitempty"`
	// +optional
	TCPTimeout metav1.Duration `json:"tcpTimeout"`
	// +optional
	TCPFinTimeout metav1.Duration `json:"tcpFinTimeout"`
	// +optional
	UDPTimeout metav1.Duration `json:"udpTimeout"`
}

// KubeProxyNFTablesConfiguration contains nftables-related kube-proxy configuration
// @see https://github.com/kubernetes/kube-proxy/blob/v0.34.3/config/v1alpha1/types.go#L82-L97
type KubeProxyNFTablesConfiguration struct {
	// +optional
	SyncPeriod    metav1.Duration `json:"syncPeriod"`
	MasqueradeBit *int32          `json:"masqueradeBit,omitempty"`
	MasqueradeAll bool            `json:"masqueradeAll,omitempty"`
	// +optional
	MinSyncPeriod metav1.Duration `json:"minSyncPeriod"`
}
