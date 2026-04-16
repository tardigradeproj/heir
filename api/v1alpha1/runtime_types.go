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

// UpstreamCluster defines how UpstreamCluster components are configured
type UpstreamCluster struct {
	// +kubebuilder:default={}
	APIServer APIServerSpec `json:"apiServer,omitempty"`
	// +kubebuilder:default={}
	ControllerManager ControllerManagerSpec `json:"controllerManager,omitempty"`
	// +kubebuilder:default={}
	Scheduler SchedulerSpec `json:"scheduler,omitempty"`
	// +kubebuilder:default={}
	Network NetworkSpec `json:"network,omitempty"`
	// +kubebuilder:default={type="kine"}
	Storage StorageSpec `json:"storage,omitempty"`
	// +kubebuilder:default={}
	ExtraResources ExtraResourcesSpec `json:"extraResources,omitempty"`
}

// APIServerSpec defines api-server configurations
type APIServerSpec struct {
	// If Samaritano controllers are running behind a loadbalancer provide the loadbalancer address here. This will configure all cluster
	// components to connect to this address and also configures this address to be used when joining new nodes into the cluster.
	// eg: https://my-cluster.io:9963
	ExternalAddress string `json:"externalAddress,omitempty"`
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
	CNI       CNISpec        `json:"cni,omitempty"`
	KubeProxy *KubeProxySpec `json:"kubeProxy,omitempty"`
}
type CNISpec struct {
	// +kubebuilder:validation:Enum=calico;custom
	//+kubebuilder:default="calico"
	Supplier string `json:"supplier,omitempty"`
}
type StorageSpec struct {
	// Type holds the type of storage to be used by APIServer
	// +kubebuilder:validation:Enum=kine
	//+kubebuilder:default="kine"
	Type string `json:"type,omitempty"`
	// Kine holds kine configuration
	Kine KineSpec `json:"kine,omitempty"`
}
type KineSpec struct {
	// DataSource holds the URL of the data source. Refer to: https://github.com/rancher/kine/
	DataSource string `json:"dataSource,omitempty"`
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
	ControlPlane    ControlPlaneSpec `json:"controlPlane,omitempty"`
	UpstreamCluster UpstreamCluster  `json:"upstreamCluster,omitempty"`
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
	ServiceType corev1.ServiceType `json:"serviceType"`
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
	//+kubebuilder:default=2
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
