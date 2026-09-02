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

package clusteragent

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// CSRApproverReconciler watches kubernetes.io/kubelet-serving CertificateSigningRequests on
// this cluster and approves those whose SANs are all already known addresses of the
// requesting Node.
type CSRApproverReconciler struct {
	client.Client
	Clientset kubernetes.Interface
	Recorder  events.EventRecorder
}

// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests,verbs=get;list;watch
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=certificatesigningrequests/approval,verbs=update
// +kubebuilder:rbac:groups=certificates.k8s.io,resources=signers,verbs=approve,resourceNames=kubernetes.io/kubelet-serving
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile validates and approves a single pending kubernetes.io/kubelet-serving CSR.
// Validation failures are logged and left pending rather than requeued: retrying an
// unauthorized requestor or a SAN/node mismatch wouldn't change the outcome, so the CSR is
// left for a human (or a corrected subsequent request) to resolve.
func (r *CSRApproverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	csr := &certificatesv1.CertificateSigningRequest{}
	if err := r.Get(ctx, req.NamespacedName, csr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !csrIsPending(csr) {
		return ctrl.Result{}, nil
	}

	if err := r.validateAndApprove(ctx, csr); err != nil {
		log.Info("skipping CSR", "csr", csr.Name, "reason", err.Error())
		if r.Recorder != nil {
			r.Recorder.Eventf(csr, nil, corev1.EventTypeWarning, "CSRApprovalSkipped", "ValidateAndApprove",
				"CSR %q not approved: %v", csr.Name, err)
		}
		return ctrl.Result{}, nil
	}

	log.Info("approved kubelet serving CSR", "csr", csr.Name)
	if r.Recorder != nil {
		r.Recorder.Eventf(csr, nil, corev1.EventTypeNormal, "CSRApproved", "ValidateAndApprove",
			"CSR %q approved: all SANs validated against node addresses", csr.Name)
	}
	return ctrl.Result{}, nil
}

// validateAndApprove requires the requestor to be a node identity (system:node:<name>),
// resolves that Node, and checks that every DNS and IP SAN in the CSR is already a known
// address of the Node before approving.
func (r *CSRApproverReconciler) validateAndApprove(ctx context.Context, csr *certificatesv1.CertificateSigningRequest) error {
	if !strings.HasPrefix(csr.Spec.Username, "system:node:") {
		return fmt.Errorf("requestor %q is not a node identity (expected system:node:<name>)", csr.Spec.Username)
	}
	nodeName := strings.TrimPrefix(csr.Spec.Username, "system:node:")

	node, err := r.Clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get node %q: %w", nodeName, err)
	}

	x509cr, err := parseCSRBytes(csr.Spec.Request)
	if err != nil {
		return fmt.Errorf("parse CSR request bytes: %w", err)
	}

	nodeAddrs := buildNodeAddressSet(node)
	for _, dns := range x509cr.DNSNames {
		if _, ok := nodeAddrs[dns]; !ok {
			return fmt.Errorf("DNS SAN %q not present in node %q addresses %v", dns, nodeName, nodeAddrs)
		}
	}
	for _, ip := range x509cr.IPAddresses {
		if _, ok := nodeAddrs[ip.String()]; !ok {
			return fmt.Errorf("IP SAN %q not present in node %q addresses %v", ip.String(), nodeName, nodeAddrs)
		}
	}

	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type:           certificatesv1.CertificateApproved,
		Status:         corev1.ConditionTrue,
		Reason:         "AutoApproved",
		Message:        "approved by heir clusteragent: all SANs validated against node addresses",
		LastUpdateTime: metav1.Now(),
	})
	if _, err := r.Clientset.CertificatesV1().CertificateSigningRequests().UpdateApproval(
		ctx, csr.Name, csr, metav1.UpdateOptions{},
	); err != nil {
		return fmt.Errorf("failed to approve csr after successful csr validation: %w", err)
	}
	return nil
}

// csrIsPending reports whether csr has not yet been approved or denied.
func csrIsPending(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certificatesv1.CertificateApproved || c.Type == certificatesv1.CertificateDenied {
			return false
		}
	}
	return true
}

// parseCSRBytes decodes a PEM-encoded "CERTIFICATE REQUEST" block and parses it as an x509 CSR.
func parseCSRBytes(pemBytes []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("no PEM block in CSR request")
	}
	return x509.ParseCertificateRequest(block.Bytes)
}

// buildNodeAddressSet returns the set of every address (hostname, internal IP, external IP,
// ...) currently reported on node's status.
func buildNodeAddressSet(node *corev1.Node) map[string]struct{} {
	addrs := make(map[string]struct{}, len(node.Status.Addresses))
	for _, a := range node.Status.Addresses {
		addrs[a.Address] = struct{}{}
	}
	return addrs
}

// SetupWithManager sets up the controller with the Manager, watching only
// kubernetes.io/kubelet-serving CertificateSigningRequests.
func (r *CSRApproverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	isKubeletServingCSR := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		csr, ok := obj.(*certificatesv1.CertificateSigningRequest)
		return ok && csr.Spec.SignerName == certificatesv1.KubeletServingSignerName
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&certificatesv1.CertificateSigningRequest{}, builder.WithPredicates(isKubeletServingCSR)).
		Named("clusteragent-csrapprover").
		Complete(r)
}
