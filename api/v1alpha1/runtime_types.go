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

// UpstreamCluster defines the configuration for the Kubernetes cluster
// running inside the control plane pod and worker nodes, including networking, storage, and
// individual component settings.
type UpstreamCluster struct {
	// ControlPlaneEndpoint describes the addresses and ports through which worker
	// nodes reach the API server.
	// +kubebuilder:default={}
	ControlPlaneEndpoint ControlPlaneEndpointSpec `json:"controlPlaneEndpoint"`
	// APIServer holds flags and SANs passed to the tenant kube-apiserver.
	// +kubebuilder:default={}
	APIServer APIServerSpec `json:"apiServer"`
	// ControllerManager holds extra flags passed to the tenant kube-controller-manager.
	// +kubebuilder:default={}
	ControllerManager ControllerManagerSpec `json:"controllerManager"`
	// Scheduler holds extra flags passed to the tenant kube-scheduler.
	// +kubebuilder:default={}
	Scheduler SchedulerSpec `json:"scheduler"`
	// Network configures pod CIDR, service CIDR, CNI plugin, kube-proxy, and CoreDNS
	// for the tenant cluster.
	// +kubebuilder:default={}
	Network NetworkSpec `json:"network"`
	// Storage configures the backend used by the tenant API server in place of etcd.
	// +kubebuilder:default={"type": "kine"}
	Storage StorageSpec `json:"storage"`
	// Kubelet holds extra flags and configuration patches applied to kubelet
	// on worker nodes that join this tenant cluster.
	// +kubebuilder:default={}
	Kubelet KubeletSpec `json:"kubelet"`
	// ExtraResources is a list of arbitrary Kubernetes objects applied to the
	// tenant cluster once its API server becomes available.
	// +kubebuilder:default={}
	ExtraResources ExtraResourcesSpec `json:"extraResources"`
}

// PlaneTunnelSpec configures the plane tunnel TCP multiplexer, which tunnels traffic
// between worker nodes and the API server.
type PlaneTunnelSpec struct {
	// server configures the plane tunnel server Deployment that runs in the management cluster.
	//+kubebuilder:default={image:"ghcr.io/tardigradeproj/heir-tunnel:latest"}
	Server PlaneTunnelSpecServerSpec `json:"server,omitempty"`
}

// PlaneTunnelSpecServerSpec configures the plane tunnel server component.
type PlaneTunnelSpecServerSpec struct {
	// image is the container image for the plane tunnel server.
	//+kubebuilder:default="ghcr.io/tardigradeproj/heir-tunnel:latest"
	Image string `json:"image,omitempty"`
	// deployment configures the Deployment resource created for the plane tunnel server pods.
	Deployment DeploymentSpec `json:"deployment,omitempty"`
}

// KubeletSpec defines configuration applied to kubelet on worker nodes that
// join this tenant cluster.
type KubeletSpec struct {
	// ExtraArgs is a map of additional flags passed directly to the kubelet process.
	// Keys are flag names (without the leading --) and values are their string representations.
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
	// ConfigPatches is a partial KubeletConfiguration document that is strategically
	// merged on top of the default kubelet configuration written to worker nodes.
	// Only the fields you specify are overridden; all others remain at their defaults.
	// +optional
	ConfigPatches string `json:"configPatches,omitempty"`
}

// ControlPlaneEndpointSpec describes the addresses and ports that worker nodes
// use to reach the tenant control plane. Each worker runs a local proxy that
// forwards traffic to these endpoints.
type ControlPlaneEndpointSpec struct {
	// addresses is the list of IP addresses or hostnames of the hosts exposing
	// the tenant control plane (e.g. the NodePort addresses of the Management cluster nodes).
	// It cannot contain protocol nor port.
	Addresses []string `json:"addresses,omitempty"`
	// apiServer defines the port on the above addresses that exposes the Kubernetes API server.
	//+kubebuilder:default={port:30080}
	APIServer ComponentEndpoint `json:"apiServer"`
}

// ComponentEndpoint holds the port of a single control-plane component endpoint.
type ComponentEndpoint struct {
	// port is the TCP port number on the remote endpoint.
	Port int32 `json:"port"`
}

// APIServerSpec defines configuration for the tenant kube-apiserver.
type APIServerSpec struct {
	// sans is a list of additional Subject Alternative Names added to the API server's
	// serving certificate. Use this to include load-balancer IPs, external hostnames,
	// or any address through which clients will reach the API server.
	Sans []string `json:"sans,omitempty"`
	// extraArgs is a map of additional flags passed directly to the kube-apiserver process.
	// Keys are flag names (without the leading --) and values are their string representations.
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}

// ControllerManagerSpec defines configuration for the tenant kube-controller-manager.
type ControllerManagerSpec struct {
	// extraArgs is a map of additional flags passed directly to the kube-controller-manager process.
	// Keys are flag names (without the leading --) and values are their string representations.
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}

// SchedulerSpec defines configuration for the tenant kube-scheduler.
type SchedulerSpec struct {
	// extraArgs is a map of additional flags passed directly to the kube-scheduler process.
	// Keys are flag names (without the leading --) and values are their string representations.
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}

// NetworkSpec defines the pod and service networking configuration for the tenant cluster.
type NetworkSpec struct {
	// podCIDR is the IP range from which pod IP addresses are allocated.
	//+kubebuilder:default="10.244.0.0/16"
	//+kubebuilder:validation:Optional
	PodCIDR string `json:"podCIDR,omitempty"`
	// serviceCIDR is the IP range from which ClusterIP service addresses are allocated.
	//+kubebuilder:default="10.96.0.0/16"
	//+kubebuilder:validation:Optional
	// +optional
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
	// cni configures the Container Network Interface plugin installed in the tenant cluster.
	// +kubebuilder:default={}
	CNI CNISpec `json:"cni,omitempty"`
	// kubeProxy configures kube-proxy in the tenant cluster.
	// +kubebuilder:default={}
	KubeProxy KubeProxySpec `json:"kubeProxy"`
	// coredns configures CoreDNS in the tenant cluster.
	// +kubebuilder:default={}
	Coredns CorednsSpec `json:"coredns"`
}

// CNISpec selects the Container Network Interface plugin to install in the tenant cluster.
type CNISpec struct {
	// supplier is the CNI plugin to install.
	// +kubebuilder:validation:Enum=flannel;custom
	//+kubebuilder:default="flannel"
	Supplier string `json:"supplier,omitempty"`
}

// StorageSpec configures the storage backend used by the tenant API server.
type StorageSpec struct {
	// type selects the storage backend. Currently only kine is supported.
	// +kubebuilder:validation:Enum=kine
	//+kubebuilder:default="kine"
	Type string `json:"type"`
	// kine holds configuration for the kine storage adapter, which provides
	// an etcd-compatible interface backed by a relational database.
	Kine *KineSpec `json:"kine"`
}

// KineSpec configures the kine storage backend.
// See https://github.com/rancher/kine for supported data source URL formats.
type KineSpec struct {
	// dataSourceRef references a Secret key whose value is the kine data source URL
	// (e.g. a SQLite file path or a PostgreSQL connection string).
	// When omitted, kine uses its default SQLite storage location.
	// +optional
	DataSourceRef *corev1.SecretKeySelector `json:"dataSourceRef,omitempty"`
}

// ExtraResourcesSpec holds a list of arbitrary Kubernetes objects to be applied
// to the tenant cluster after it becomes available.
type ExtraResourcesSpec struct {
	// objects is a list of raw Kubernetes resource manifests applied to the tenant
	// cluster on startup. Any valid Kubernetes object is accepted.
	// +optional
	Objects []runtime.RawExtension `json:"objects,omitempty"`
}

// RuntimeSpec defines the desired state of Runtime.
type RuntimeSpec struct {
	// controlPlane configures how the tenant control plane is deployed in the management cluster.
	// +kubebuilder:default={}
	ControlPlane ControlPlaneSpec `json:"controlPlane,omitempty"`
	// upstreamCluster configures the tenant Kubernetes cluster running inside the control plane pod.
	// +kubebuilder:default={}
	UpstreamCluster UpstreamCluster `json:"upstreamCluster,omitempty"`
}

// ControlPlaneSpec defines how the tenant control plane is deployed in the management cluster,
// including the container image, Deployment, and Service configuration.
type ControlPlaneSpec struct {
	// heir specifies the Heir distribution image to run as the heir control plane.
	// +required
	Heir HeirSpec `json:"heir,omitempty"`
	// deployment configures the Deployment resource created for the heir control plane pods.
	Deployment DeploymentSpec `json:"deployment,omitempty"`
	// service configures the Service resource that exposes the tenant control plane.
	Service ServiceSpec `json:"service,omitempty"`
	// planeTunnel configures the plane tunnel server Deployment and Service.
	// +kubebuilder:default={}
	PlaneTunnel PlaneTunnelSpec `json:"planeTunnel,omitempty"`
}

// HeirSpec identifies the Heir distribution image used for the tenant control plane.
type HeirSpec struct {
	// image is the fully qualified container image reference (including tag or digest)
	// for the Heir control plane, e.g. ghcr.io/tardigradeproj/heir:v1.2.3.
	Image string `json:"image,omitempty"`
}

// ServiceSpec configures the Kubernetes Service that exposes the tenant control plane.
type ServiceSpec struct {
	// additionalMetadata allows attaching extra labels and annotations to the generated Service.
	AdditionalMetadata AdditionalMetadata `json:"additionalMetadata,omitempty"`
	// additionalPorts adds extra ports to the Service, in addition to the default API server port.
	AdditionalPorts []AdditionalPort `json:"additionalPorts,omitempty"`
	// serviceType controls how the Service is exposed.
	//+kubebuilder:validation:Enum=NodePort;ClusterIP;LoadBalancer;ExternalName
	//+kubebuilder:default="NodePort"
	ServiceType corev1.ServiceType `json:"serviceType"`
	// apiServerNodePort is the NodePort assigned to the API server port.
	// Only used when serviceType is NodePort.
	//+kubebuilder:default=30080
	ApiServerNodePort int32 `json:"apiServerNodePort"`
	// PlaneTunnelNodePort is the NodePort assigned to the plane tunnel proxy port.
	// Only used when serviceType is NodePort.
	//+kubebuilder:default=30081
	PlaneTunnelNodePort int32 `json:"planeTunnelNodePort"`
}

// AdditionalPort defines an extra port to add to a Service.
type AdditionalPort struct {
	// name is the port name within the Service. Must be unique across all ports.
	Name string `json:"name"`
	// protocol is the IP protocol for this port.
	//+kubebuilder:validation:Enum=TCP;UDP;SCTP
	//+kubebuilder:default=TCP
	Protocol corev1.Protocol `json:"protocol,omitempty"`
	// appProtocol is a hint to implementations about the application-layer protocol
	// carried on this port (e.g. "http", "https"). Follows Kubernetes label syntax.
	// +optional
	AppProtocol *string `json:"appProtocol,omitempty"`
	// port is the port number exposed by the Service.
	Port int32 `json:"port"`
	// targetPort is the port number or named port on the tenant control plane pods
	// that receives traffic for this Service port.
	// +optional
	TargetPort intstr.IntOrString `json:"targetPort"`
}

// AdditionalMetadata holds extra labels and annotations to attach to a generated resource.
type AdditionalMetadata struct {
	// labels are added to the resource's metadata.labels map.
	Labels map[string]string `json:"labels,omitempty"`
	// annotations are added to the resource's metadata.annotations map.
	Annotations map[string]string `json:"annotations,omitempty"`
}

// DeploymentSpec configures the Deployment created for the tenant control plane pods.
type DeploymentSpec struct {
	// additionalMetadata allows attaching extra labels and annotations to the generated Deployment.
	AdditionalMetadata AdditionalMetadata `json:"additionalMetadata,omitempty"`
	// replicas is the desired number of tenant control plane pod replicas.
	//+kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`
	// runtimeClassName references a RuntimeClass object that controls which container
	// runtime is used for the tenant control plane pods. When empty, the cluster default is used.
	RuntimeClassName string `json:"runtimeClassName,omitempty"`
	// tolerations applied to the tenant control plane pods.
	// More info: https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// affinity defines scheduling constraints for the tenant control plane pods.
	// More info: https://kubernetes.io/docs/tasks/configure-pod-container/assign-pods-nodes-using-node-affinity/
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
	// serviceAccountName is the name of the ServiceAccount mounted into the tenant control plane pods.
	//+kubebuilder:default="default"
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
	// resources defines the CPU and memory requests and limits for the container.
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// RegistrySettings configures a container image registry reference,
// including the registry host, image name, and pull policy.
type RegistrySettings struct {
	// registry is the container registry hostname, e.g. registry.k8s.io.
	//+kubebuilder:default="registry.k8s.io"
	Registry string `json:"registry,omitempty"`
	// image is the image name within the registry, without the tag or digest.
	Image string `json:"image,omitempty"`
	// pullPolicy controls when the kubelet pulls the image.
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`
}

// RuntimeStatus defines the observed state of Runtime.
type RuntimeStatus struct {
	// conditions reflect the current status of the Runtime.
	// Standard types are:
	//   Available   — the control plane is fully operational.
	//   Progressing — the control plane is being created or updated.
	//   Degraded    — the control plane failed to reach or maintain its desired state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// certificatesExpireAt is the time at which the PKI certificates stored in the -pki Secret will expire.
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
