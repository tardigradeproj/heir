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
)

// WorkerJoinTokenSpec defines the desired state of WorkerJoinToken.
type WorkerJoinTokenSpec struct {
	// RuntimeRef names the Runtime (tenant control plane) this token grants access
	// to join. Must exist in the same namespace as this WorkerJoinToken. Immutable
	// after creation: retargeting a token at a different Runtime is a different
	// resource, not an update to this one — delete and recreate instead.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="runtimeRef is immutable"
	RuntimeRef corev1.LocalObjectReference `json:"runtimeRef"`

	// TTL is how long a minted token remains valid on the tenant cluster, starting
	// from the moment it is issued. Immutable after creation: this resource is
	// intentionally one-shot, not auto-renewing, so the only way to mint a token with
	// a different TTL is to delete and recreate the object.
	//
	// The immutability rule compares parsed durations, not raw strings: metav1.Duration
	// round-trips "3h" as "3h0m0s" on any read-modify-write (e.g. the controller adding
	// its finalizer), which would spuriously fail a plain self == oldSelf string check.
	// +kubebuilder:default="3h"
	// +kubebuilder:validation:XValidation:rule="duration(self) == duration(oldSelf)",message="ttl is immutable"
	TTL metav1.Duration `json:"ttl,omitempty"`
}

// WorkerJoinTokenStatus defines the observed state of WorkerJoinToken.
type WorkerJoinTokenStatus struct {
	// conditions represent the current state of the WorkerJoinToken resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types are:
	//   Ready    — a non-expired token and kubeconfig Secret are available.
	//   Degraded — the controller could not reach the tenant cluster or mint a token.
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TokenID is the public identifier of the currently issued bootstrap token (the
	// secret half is never surfaced on the object itself, only inside SecretRef).
	// +optional
	TokenID string `json:"tokenID,omitempty"`

	// ExpiresAt is when the currently issued token expires on the tenant cluster.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// SecretRef names the Secret, in this object's namespace, holding the
	// base64-encoded bootstrap kubeconfig under key "kubeconfig". Owned by this
	// WorkerJoinToken; garbage-collected alongside it.
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

	// SecretChecksum is the sha256 checksum of the "kubeconfig" value the controller
	// wrote into SecretRef. Compared against the Secret's live content on every
	// reconcile so drift (edits or deletion of the Secret) is detected and repaired by
	// minting a replacement token, not just papered over.
	// +optional
	SecretChecksum string `json:"secretChecksum,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Runtime",type="string",JSONPath=".spec.runtimeRef.name"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Expires",type="date",JSONPath=".status.expiresAt"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// WorkerJoinToken is the Schema for the workerjointokens API
type WorkerJoinToken struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of WorkerJoinToken
	// +required
	Spec WorkerJoinTokenSpec `json:"spec"`

	// status defines the observed state of WorkerJoinToken
	// +optional
	Status WorkerJoinTokenStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// WorkerJoinTokenList contains a list of WorkerJoinToken
type WorkerJoinTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []WorkerJoinToken `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkerJoinToken{}, &WorkerJoinTokenList{})
}
