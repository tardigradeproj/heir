/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// #TODO:  create property for clusterDomain (default: cluster.local)

// UpstreamCluster defines how UpstreamCluster components are configured
type UpstreamCluster struct {
	// +kubebuilder:default={}
	ControlPlaneEndpoint ControlPlaneEndpointSpec `json:"controlPlaneEndpoint"`
	// +kubebuilder:default={}
	APIServer APIServerSpec `json:"apiServer"`
	// +kubebuilder:default={}
	ControllerManager ControllerManagerSpec `json:"controllerManager"`
	// +kubebuilder:default={}
	Scheduler SchedulerSpec `json:"scheduler"`
	// +kubebuilder:default={}
	Network NetworkSpec `json:"network"`
	// +kubebuilder:default={"type": "kine"}
	Storage StorageSpec `json:"storage"`
	// +kubebuilder:default={}
	Kubelet KubeletSpec `json:"kubelet"`
	// +kubebuilder:default={}
	ExtraResources ExtraResourcesSpec `json:"extraResources"`
}
type KubeletSpec struct {
	// extraArgs defines a Map of key-values (strings) for any extra arguments you wish to pass down to kubelet
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
	// configuration holds KubeletConfiguration. The configuration provided patches the default kubelet configuration
	// the kubelet config file on worker nodes.
	// +optional
	ConfigPatches string `json:"configPatches,omitempty"`
}

// ControlPlaneEndpointSpec describes how worker nodes reach
// control plane components. The local LB on each worker
// always listens on fixed local ports (6443, 8132) — this
// struct defines what it proxies TO.

type ControlPlaneEndpointSpec struct {
	// Addresses are the hosts (IP or hostname) to connect to.
	Addresses []string `json:"addresses,omitempty"`
	// APIServer defines how workers reach the API server.
	//+kubebuilder:default={port:30080}
	APIServer ComponentEndpoint `json:"apiServer"`
	// Konnectivity defines how workers reach the Konnectivity server.
	//+kubebuilder:default={port:30081}
	Konnectivity ComponentEndpoint `json:"konnectivity"`
}
type ComponentEndpoint struct {
	// Port on the remote endpoint.
	Port int32 `json:"port"`
}

// APIServerSpec defines api-server configurations
type APIServerSpec struct {
	// sans defines a List of additional addresses to push to API servers serving certificate
	Sans []string `json:"sans,omitempty"`
	// extraArgs defines a Map of key-values (strings) for any extra arguments you wish to pass down to Kubernetes api-server process
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}
type ControllerManagerSpec struct {
	// extraArgs defines a Map of key-values (strings) for any extra arguments you wish to pass down to Kubernetes controller manager process
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}
type SchedulerSpec struct {
	// extraArgs defines a Map of key-values (strings) for any extra arguments you wish to pass down to Kubernetes scheduler process
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}
type NetworkSpec struct {
	// CIDR for Kubernetes Pods: if empty, defaulted to 10.244.0.0/16.
	//+kubebuilder:default="10.244.0.0/16"
	//+kubebuilder:validation:Optional
	PodCIDR string `json:"podCIDR,omitempty"`
	// CIDR for Kubernetes Services: if empty, defaulted to 10.96.0.0/16.
	//+kubebuilder:default="10.96.0.0/16"
	//+kubebuilder:validation:Optional
	// +optional
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
	// CNI configuration
	// +kubebuilder:default={}
	CNI CNISpec `json:"cni,omitempty"`
	// +kubebuilder:default={}
	KubeProxy KubeProxySpec `json:"kubeProxy"`
	// +kubebuilder:default={}
	Coredns CorednsSpec `json:"coredns"`
	// Enables the Konnectivity addon in the Tenant Cluster, required if the worker nodes are in a different network.
	// +kubebuilder:default={enabled:true}
	Konnectivity KonnectivitySpec `json:"konnectivity"`
}

// KonnectivitySpec defines the spec for Konnectivity.
type KonnectivitySpec struct {
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`
	//+kubebuilder:default={image:"registry.k8s.io/kas-network-proxy/proxy-server:v0.0.37",port:8132}
	KonnectivityServerSpec KonnectivityServerSpec `json:"server,omitempty"`
	//+kubebuilder:default={image:"registry.k8s.io/kas-network-proxy/proxy-agent:v0.0.37",mode:"DaemonSet"}
	KonnectivityAgentSpec KonnectivityAgentSpec `json:"agent,omitempty"`
}
type KonnectivityServerSpec struct {
	// Container image used by the Konnectivity server.
	//+kubebuilder:default="registry.k8s.io/kas-network-proxy/proxy-server:v0.0.37"
	Image string `json:"image,omitempty"`
	// Resources define the amount of CPU and memory to allocate to the Konnectivity server.
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
	ExtraArgs map[string]string            `json:"extraArgs,omitempty"`
}
type KonnectivityAgentMode string

var (
	KonnectivityAgentModeDaemonSet KonnectivityAgentMode = "DaemonSet"
)

type KonnectivityAgentSpec struct {
	// AgentImage defines the container image for Konnectivity's agent.
	//+kubebuilder:default="registry.k8s.io/kas-network-proxy/proxy-agent:v0.0.37"
	Image string `json:"image,omitempty"`
	// Tolerations for the deployed agent.
	// Can be customized to start the konnectivity-agent even if the nodes are not ready or tainted.
	//+kubebuilder:default={{key: "CriticalAddonsOnly", operator: "Exists"}}
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	ExtraArgs   map[string]string   `json:"extraArgs,omitempty"`
	// HostNetwork enables the konnectivity agent to use the Host network namespace.
	// By enabling this mode, the Agent doesn't need to wait for the CNI initialisation,
	// enabling a sort of out-of-band access to nodes for troubleshooting scenarios,
	// or when the agent needs direct access to the host network.
	//+kubebuilder:default=true
	HostNetwork bool `json:"hostNetwork,omitempty"`
	// Mode allows specifying the Agent deployment mode: Deployment, or DaemonSet (default).
	//+kubebuilder:default="DaemonSet"
	//+kubebuilder:validation:Enum=DaemonSet;Deployment
	Mode KonnectivityAgentMode `json:"mode,omitempty"`
	// Replicas defines the number of replicas when Mode is Deployment.
	// Must be 0 if Mode is DaemonSet.
	//+kubebuilder:validation:Optional
	Replicas *int32 `json:"replicas,omitempty"`
}
type CNISpec struct {
	// +kubebuilder:validation:Enum=flannel;custom
	//+kubebuilder:default="flannel"
	Supplier string `json:"supplier,omitempty"`
}
type StorageSpec struct {
	// Type holds the type of storage to be used by APIServer
	// +kubebuilder:validation:Enum=kine
	//+kubebuilder:default="kine"
	Type string `json:"type"`
	// Kine holds kine configuration
	Kine *KineSpec `json:"kine"`
}
type KineSpec struct {
	// DataSourceRef points to a Secret containing the Kine data source URL.
	// This prevents sensitive credentials from being exposed in plain text.
	// If omitted, the system will default to the standard Kine storage mechanism.
	// Refer to: https://github.com/rancher/kine/
	// +optional
	DataSourceRef *corev1.SecretKeySelector `json:"dataSourceRef,omitempty"`
}

// ExtraResourcesSpec holds a list of arbitrary Kubernetes objects to be applied
// to the upstream cluster on startup.
type ExtraResourcesSpec struct {
	// Objects is a list of Kubernetes objects to apply on cluster startup.
	// Any valid Kubernetes resource manifest is accepted.
	// +optional
	Objects []runtime.RawExtension `json:"objects,omitempty"`
}

// RuntimeSpec defines the desired state of Runtime
type RuntimeSpec struct {
	// +kubebuilder:default={}
	ControlPlane ControlPlaneSpec `json:"controlPlane,omitempty"`
	// +kubebuilder:default={}
	UpstreamCluster UpstreamCluster `json:"upstreamCluster,omitempty"`
}

// ControlPlaneSpec defines how control plane must be created in the Admin UpstreamCluster,
// such as the number of Pod replicas, the Service resource, or the Ingress.
type ControlPlaneSpec struct {
	// Samaritano provides details about the distribution to install
	// +required
	Samaritano SamaritanoSpec `json:"samaritano,omitempty"`
	// Defining the options for Deployment resource.
	Deployment DeploymentSpec `json:"deployment,omitempty"`
	// Defining the options for an Optional Ingress which will expose API Server of the Tenant Control Plane
	Ingress *IngressSpec `json:"ingress,omitempty"`
	Service ServiceSpec  `json:"service,omitempty"`
}
type SamaritanoSpec struct {
	Image string `json:"image,omitempty"`
}
type IngressSpec struct {
	AdditionalMetadata AdditionalMetadata `json:"additionalMetadata,omitempty"`
	IngressClassName   string             `json:"ingressClassName,omitempty"`
	Hostname           string             `json:"hostname,omitempty"`
}
type ServiceSpec struct {
	AdditionalMetadata AdditionalMetadata `json:"additionalMetadata,omitempty"`
	// AdditionalPorts allows adding additional ports to the Service generated
	// which targets the Tenant Control Plane pods.
	AdditionalPorts []AdditionalPort `json:"additionalPorts,omitempty"`
	// ServiceType allows specifying how to expose the Control Plane.
	//+kubebuilder:validation:Enum=NodePort;ClusterIP;LoadBalancer;ExternalName
	//+kubebuilder:default="NodePort"
	ServiceType corev1.ServiceType `json:"serviceType"`
	// ApiServer NodePort, only use this option when serviceType is NodePort
	//+kubebuilder:default=30080
	ApiServerNodePort int32 `json:"apiServerNodePort"`
	// Konnectivity NodePort, only use this option when serviceType is NodePort
	//+kubebuilder:default=30081
	KonnectivityNodePort int32 `json:"KonnectivityNodePort"`
}
type AdditionalPort struct {
	// The name of this port within the Service created by tardigrade.
	Name string `json:"name"`
	// The IP protocol for this port. Supports "TCP", "UDP", and "SCTP".
	//+kubebuilder:validation:Enum=TCP;UDP;SCTP
	//+kubebuilder:default=TCP
	Protocol corev1.Protocol `json:"protocol,omitempty"`
	// The application protocol for this port.
	// This is used as a hint for implementations to offer richer behavior for protocols that they understand.
	// This field follows standard Kubernetes label syntax.
	// Valid values are either:
	//
	// * Un-prefixed protocol names - reserved for IANA standard service names (as per
	// RFC-6335 and https://www.iana.org/assignments/service-names).
	// +optional
	AppProtocol *string `json:"appProtocol,omitempty"`
	// The port that will be exposed by this service.
	Port int32 `json:"port"`
	// Number or name of the port to access on the pods of the Tenant Control Plane.
	// Number must be in the range 1 to 65535. Name must be an IANA_SVC_NAME.
	// If this is a string, it will be looked up as a named port in the
	// target Pod's container ports. If this is not specified, the value
	// of the 'port' field is used (an identity map).
	// +optional
	TargetPort intstr.IntOrString `json:"targetPort"`
}
type AdditionalMetadata struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}
type DeploymentSpec struct {
	AdditionalMetadata AdditionalMetadata `json:"additionalMetadata,omitempty"`
	// RegistrySettings allows to override the default images for the given Runtime instance.
	// It could be used to point to a different container registry rather than the public one.
	// +optional
	RegistrySettings RegistrySettings `json:"registrySettings,omitempty"`
	//+kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`
	// RuntimeClassName refers to a RuntimeClass object in the node.k8s.io group, which should be used
	// to run the Tenant Control Plane pod. If no RuntimeClass resource matches the named class, the pod will not be run.
	// If unset or empty, the "legacy" RuntimeClass will be used, which is an implicit class with an
	// empty definition that uses the default runtime handler.
	RuntimeClassName string `json:"runtimeClassName,omitempty"`
	// If specified, the Tenant Control Plane pod's tolerations.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// If specified, the Tenant Control Plane pod's scheduling constraints.
	// More info: https://kubernetes.io/docs/tasks/configure-pod-container/assign-pods-nodes-using-node-affinity/
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
	//+kubebuilder:default="default"
	// ServiceAccountName allows to specify the service account to be mounted to the pods of the Control plane deployment
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// #TODO: review
type RegistrySettings struct {
	//+kubebuilder:default="registry.k8s.io"
	Registry   string            `json:"registry,omitempty"`
	Image      string            `json:"image,omitempty"`
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`
}

// RuntimeStatus defines the observed state of Runtime.
type RuntimeStatus struct {

	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// CertificatesExpireAt is the time when the PKI certificates stored in the -pki secret
	CertificatesExpireAt *metav1.Time `json:"certificatesExpireAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Runtime is the Schema for the runtimes API
type Runtime struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Runtime
	// +required
	Spec RuntimeSpec `json:"spec"`

	// status defines the observed state of Runtime
	// +optional
	Status RuntimeStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RuntimeList contains a list of Runtime
type RuntimeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Runtime `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Runtime{}, &RuntimeList{})
}
