package masteragent

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tardigradeproj/heir/pkg/k8s"
	pkgruntime "github.com/tardigradeproj/heir/pkg/runtime"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const csrSyncInterval = 15 * time.Second

// CSRAutoApprover periodically lists pending kubernetes.io/kubelet-serving CSRs,
// validates that every SAN in the request is a known address of the requesting node,
// and approves those that pass.
type CSRAutoApprover struct {
	client kubernetes.Interface
}

func NewCSRAutoApprover(client kubernetes.Interface) *CSRAutoApprover {
	return &CSRAutoApprover{client: client}
}

// RunCSRAutoApprover builds a client from the in-cluster admin kubeconfig and
// runs the CSRAutoApprover until ctx is cancelled.
func RunCSRAutoApprover(ctx context.Context) error {
	layout := pkgruntime.NewControlPlaneLayout()
	client, err := k8s.BuildClient(layout.Auth.AdminConf.MountPath, "")
	if err != nil {
		return fmt.Errorf("build client for csr approver: %w", err)
	}
	return NewCSRAutoApprover(client).Run(ctx)
}

func (a *CSRAutoApprover) Run(ctx context.Context) error {
	if err := a.approveKubeletServingCSRs(ctx); err != nil {
		log.WithError(err).Error("initial CSR approval run failed")
	}

	ticker := time.NewTicker(csrSyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := a.approveKubeletServingCSRs(ctx); err != nil {
				log.WithError(err).Error("CSR approval run failed")
			}
		}
	}
}

func (a *CSRAutoApprover) approveKubeletServingCSRs(ctx context.Context) error {
	csrList, err := a.client.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{
		FieldSelector: "spec.signerName=kubernetes.io/kubelet-serving",
	})
	if err != nil {
		return fmt.Errorf("list CSRs: %w", err)
	}
	log.WithField("count", len(csrList.Items)).Info("running CSR approval cycle")
	for i := range csrList.Items {
		csr := &csrList.Items[i]
		if !csrIsPending(csr) {
			continue
		}
		if err := a.validateAndApprove(ctx, csr); err != nil {
			log.WithError(err).WithField("csr", csr.Name).Warn("skipping CSR")
		}
	}
	return nil
}

func (a *CSRAutoApprover) validateAndApprove(ctx context.Context, csr *certificatesv1.CertificateSigningRequest) error {
	logger := log.WithField("csr", csr.Name).WithField("username", csr.Spec.Username)

	if !strings.HasPrefix(csr.Spec.Username, "system:node:") {
		return fmt.Errorf("requestor %q is not a node identity (expected system:node:<name>)", csr.Spec.Username)
	}
	nodeName := strings.TrimPrefix(csr.Spec.Username, "system:node:")

	node, err := a.client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
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
		Message:        "approved by samaritano masteragent: all SANs validated against node addresses",
		LastUpdateTime: metav1.Now(),
	})
	if _, err := a.client.CertificatesV1().CertificateSigningRequests().UpdateApproval(
		ctx, csr.Name, csr, metav1.UpdateOptions{},
	); err != nil {
		return fmt.Errorf("failed to approve csr after successful csr validation: %w", err)
	}

	logger.WithField("node", nodeName).Info("approved kubelet serving CSR")
	return nil
}

func csrIsPending(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, c := range csr.Status.Conditions {
		if c.Type == certificatesv1.CertificateApproved || c.Type == certificatesv1.CertificateDenied {
			return false
		}
	}
	return true
}

func parseCSRBytes(pemBytes []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("no PEM block in CSR request")
	}
	return x509.ParseCertificateRequest(block.Bytes)
}

func buildNodeAddressSet(node *corev1.Node) map[string]struct{} {
	addrs := make(map[string]struct{}, len(node.Status.Addresses))
	for _, a := range node.Status.Addresses {
		addrs[a.Address] = struct{}{}
	}
	return addrs
}
