package masteragent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// generateServingCSRPEM builds a PEM-encoded CERTIFICATE REQUEST with the given SANs.
func generateServingCSRPEM(t *testing.T, dnsNames []string, ips []net.IP) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:     pkix.Name{CommonName: "kubelet-serving"},
		DNSNames:    dnsNames,
		IPAddresses: ips,
	}, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func newCSR(name, username, signerName string, request []byte, conds ...certificatesv1.CertificateSigningRequestCondition) *certificatesv1.CertificateSigningRequest {
	return &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Username:   username,
			SignerName: signerName,
			Request:    request,
			Usages:     []certificatesv1.KeyUsage{certificatesv1.UsageServerAuth, certificatesv1.UsageDigitalSignature},
		},
		Status: certificatesv1.CertificateSigningRequestStatus{Conditions: conds},
	}
}

func newNode(name string, addrs ...corev1.NodeAddress) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Addresses: addrs},
	}
}

func addr(t corev1.NodeAddressType, a string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: t, Address: a}
}

func countApprovals(fc *fake.Clientset) int {
	n := 0
	for _, a := range fc.Actions() {
		if a.GetVerb() == "update" && a.GetSubresource() == "approval" {
			n++
		}
	}
	return n
}

func approvedCSR(fc *fake.Clientset) *certificatesv1.CertificateSigningRequest {
	for _, a := range fc.Actions() {
		if a.GetVerb() == "update" && a.GetSubresource() == "approval" {
			return a.(k8stesting.UpdateAction).GetObject().(*certificatesv1.CertificateSigningRequest)
		}
	}
	return nil
}

// ---- csrIsPending ----

func TestCSRIsPending(t *testing.T) {
	tests := []struct {
		name       string
		conditions []certificatesv1.CertificateSigningRequestCondition
		want       bool
	}{
		{
			name: "no conditions → pending",
			want: true,
		},
		{
			name:       "approved → not pending",
			conditions: []certificatesv1.CertificateSigningRequestCondition{{Type: certificatesv1.CertificateApproved}},
			want:       false,
		},
		{
			name:       "denied → not pending",
			conditions: []certificatesv1.CertificateSigningRequestCondition{{Type: certificatesv1.CertificateDenied}},
			want:       false,
		},
		{
			name:       "failed alone → still pending",
			conditions: []certificatesv1.CertificateSigningRequestCondition{{Type: certificatesv1.CertificateFailed}},
			want:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			csr := &certificatesv1.CertificateSigningRequest{
				Status: certificatesv1.CertificateSigningRequestStatus{Conditions: tt.conditions},
			}
			assert.Equal(t, tt.want, csrIsPending(csr))
		})
	}
}

// ---- parseCSRBytes ----

func TestParseCSRBytes(t *testing.T) {
	validPEM := generateServingCSRPEM(t, []string{"node1"}, []net.IP{net.ParseIP("10.0.0.1")})

	tests := []struct {
		name        string
		input       []byte
		wantErr     bool
		errContains string
		validate    func(t *testing.T, cr *x509.CertificateRequest)
	}{
		{
			name:  "valid CSR PEM with DNS and IP SANs is parsed",
			input: validPEM,
			validate: func(t *testing.T, cr *x509.CertificateRequest) {
				assert.Equal(t, []string{"node1"}, cr.DNSNames)
				require.Len(t, cr.IPAddresses, 1)
				assert.Equal(t, "10.0.0.1", cr.IPAddresses[0].String())
			},
		},
		{
			name:        "empty bytes returns error",
			input:       []byte{},
			wantErr:     true,
			errContains: "no PEM block",
		},
		{
			name:        "non-PEM bytes returns error",
			input:       []byte("this is not PEM"),
			wantErr:     true,
			errContains: "no PEM block",
		},
		{
			name:        "wrong PEM block type returns error",
			input:       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not a csr")}),
			wantErr:     true,
			errContains: "no PEM block",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr, err := parseCSRBytes(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, cr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cr)
			tt.validate(t, cr)
		})
	}
}

// ---- buildNodeAddressSet ----

func TestBuildNodeAddressSet(t *testing.T) {
	tests := []struct {
		name     string
		addrs    []corev1.NodeAddress
		expected map[string]struct{}
	}{
		{
			name:     "node with no addresses → empty set",
			addrs:    nil,
			expected: map[string]struct{}{},
		},
		{
			name: "hostname and internal IP are both included",
			addrs: []corev1.NodeAddress{
				addr(corev1.NodeHostName, "node1"),
				addr(corev1.NodeInternalIP, "10.0.0.1"),
			},
			expected: map[string]struct{}{"node1": {}, "10.0.0.1": {}},
		},
		{
			name: "external IP is included",
			addrs: []corev1.NodeAddress{
				addr(corev1.NodeExternalIP, "1.2.3.4"),
			},
			expected: map[string]struct{}{"1.2.3.4": {}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNodeAddressSet(newNode("n", tt.addrs...))
			assert.Equal(t, tt.expected, got)
		})
	}
}

// ---- validateAndApprove ----

func TestValidateAndApprove(t *testing.T) {
	node1 := newNode("node1",
		addr(corev1.NodeHostName, "node1"),
		addr(corev1.NodeInternalIP, "10.0.0.1"),
	)
	validRequest := generateServingCSRPEM(t, []string{"node1"}, []net.IP{net.ParseIP("10.0.0.1")})

	tests := []struct {
		name        string
		csr         *certificatesv1.CertificateSigningRequest
		extraObjs   []runtime.Object
		wantErr     bool
		errContains string
		validate    func(t *testing.T, fc *fake.Clientset)
	}{
		{
			name:        "non-node username is rejected before any API call",
			csr:         newCSR("csr1", "system:serviceaccount:kube-system:sa", certificatesv1.KubeletServingSignerName, validRequest),
			wantErr:     true,
			errContains: "not a node identity",
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Equal(t, 0, countApprovals(fc))
			},
		},
		{
			name:        "node not found in cluster returns error",
			csr:         newCSR("csr1", "system:node:ghost", certificatesv1.KubeletServingSignerName, validRequest),
			wantErr:     true,
			errContains: "get node",
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Equal(t, 0, countApprovals(fc))
			},
		},
		{
			name:        "unparseable CSR request bytes returns error",
			csr:         newCSR("csr1", "system:node:node1", certificatesv1.KubeletServingSignerName, []byte("garbage")),
			extraObjs:   []runtime.Object{node1},
			wantErr:     true,
			errContains: "parse CSR",
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Equal(t, 0, countApprovals(fc))
			},
		},
		{
			name: "DNS SAN not in node addresses is rejected",
			csr: newCSR("csr1", "system:node:node1", certificatesv1.KubeletServingSignerName,
				generateServingCSRPEM(t, []string{"unknown.host"}, nil)),
			extraObjs:   []runtime.Object{node1},
			wantErr:     true,
			errContains: "DNS SAN",
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Equal(t, 0, countApprovals(fc))
			},
		},
		{
			name: "IP SAN not in node addresses is rejected",
			csr: newCSR("csr1", "system:node:node1", certificatesv1.KubeletServingSignerName,
				generateServingCSRPEM(t, nil, []net.IP{net.ParseIP("192.168.99.1")})),
			extraObjs:   []runtime.Object{node1},
			wantErr:     true,
			errContains: "IP SAN",
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Equal(t, 0, countApprovals(fc))
			},
		},
		{
			name:      "valid CSR with all SANs matching node addresses is approved",
			csr:       newCSR("csr1", "system:node:node1", certificatesv1.KubeletServingSignerName, validRequest),
			extraObjs: []runtime.Object{node1},
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Equal(t, 1, countApprovals(fc))
				updated := approvedCSR(fc)
				require.NotNil(t, updated)
				var found bool
				for _, c := range updated.Status.Conditions {
					if c.Type == certificatesv1.CertificateApproved {
						found = true
						assert.Equal(t, corev1.ConditionTrue, c.Status)
						assert.Equal(t, "AutoApproved", c.Reason)
					}
				}
				assert.True(t, found, "CertificateApproved condition must be present")
			},
		},
		{
			name: "CSR with only DNS SANs matching node hostname is approved",
			csr: newCSR("csr2", "system:node:node1", certificatesv1.KubeletServingSignerName,
				generateServingCSRPEM(t, []string{"node1"}, nil)),
			extraObjs: []runtime.Object{node1},
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Equal(t, 1, countApprovals(fc))
			},
		},
		{
			name: "CSR with only IP SANs matching node internal IP is approved",
			csr: newCSR("csr3", "system:node:node1", certificatesv1.KubeletServingSignerName,
				generateServingCSRPEM(t, nil, []net.IP{net.ParseIP("10.0.0.1")})),
			extraObjs: []runtime.Object{node1},
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Equal(t, 1, countApprovals(fc))
			},
		},
		{
			name:        "UpdateApproval API error is surfaced",
			csr:         newCSR("csr1", "system:node:node1", certificatesv1.KubeletServingSignerName, validRequest),
			extraObjs:   []runtime.Object{node1},
			wantErr:     true,
			errContains: "failed to approve csr after successful csr validation",
			validate: func(t *testing.T, fc *fake.Clientset) {
				// The update was attempted (action recorded) even though the reactor rejected it.
				assert.Equal(t, 1, countApprovals(fc), "approval must have been attempted")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := append([]runtime.Object{tt.csr}, tt.extraObjs...)
			fc := fake.NewClientset(objs...)

			if tt.name == "UpdateApproval API error is surfaced" {
				fc.Fake.PrependReactor("update", "certificatesigningrequests",
					func(action k8stesting.Action) (bool, runtime.Object, error) {
						if action.GetSubresource() == "approval" {
							return true, nil, assert.AnError
						}
						return false, nil, nil
					},
				)
			}

			approver := NewCSRAutoApprover(fc)
			err := approver.validateAndApprove(context.Background(), tt.csr)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
			tt.validate(t, fc)
		})
	}
}

// ---- approveKubeletServingCSRs ----

func TestApproveKubeletServingCSRs(t *testing.T) {
	node1 := newNode("node1",
		addr(corev1.NodeHostName, "node1"),
		addr(corev1.NodeInternalIP, "10.0.0.1"),
	)
	validRequest := generateServingCSRPEM(t, []string{"node1"}, []net.IP{net.ParseIP("10.0.0.1")})

	tests := []struct {
		name          string
		objects       []runtime.Object
		wantApprovals int
		validate      func(t *testing.T, fc *fake.Clientset)
	}{
		{
			name: "valid pending kubelet-serving CSR is approved",
			objects: []runtime.Object{
				node1,
				newCSR("csr1", "system:node:node1", certificatesv1.KubeletServingSignerName, validRequest),
			},
			wantApprovals: 1,
			validate: func(t *testing.T, fc *fake.Clientset) {
				updated := approvedCSR(fc)
				require.NotNil(t, updated)
				assert.Equal(t, "csr1", updated.Name)
			},
		},
		{
			name: "already approved CSR is skipped",
			objects: []runtime.Object{
				node1,
				newCSR("csr1", "system:node:node1", certificatesv1.KubeletServingSignerName, validRequest,
					certificatesv1.CertificateSigningRequestCondition{Type: certificatesv1.CertificateApproved},
				),
			},
			wantApprovals: 0,
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Nil(t, approvedCSR(fc))
			},
		},
		{
			name: "already denied CSR is skipped",
			objects: []runtime.Object{
				node1,
				newCSR("csr1", "system:node:node1", certificatesv1.KubeletServingSignerName, validRequest,
					certificatesv1.CertificateSigningRequestCondition{Type: certificatesv1.CertificateDenied},
				),
			},
			wantApprovals: 0,
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Nil(t, approvedCSR(fc))
			},
		},
		{
			name: "CSR with SAN not in node addresses is skipped",
			objects: []runtime.Object{
				node1,
				newCSR("csr1", "system:node:node1", certificatesv1.KubeletServingSignerName,
					generateServingCSRPEM(t, []string{"attacker.example.com"}, nil)),
			},
			wantApprovals: 0,
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Nil(t, approvedCSR(fc))
			},
		},
		{
			name:          "no CSRs in cluster → no approvals",
			objects:       []runtime.Object{node1},
			wantApprovals: 0,
			validate: func(t *testing.T, fc *fake.Clientset) {
				assert.Nil(t, approvedCSR(fc))
			},
		},
		{
			name: "only the valid pending CSR among several is approved",
			objects: []runtime.Object{
				node1,
				newCSR("csr-valid", "system:node:node1", certificatesv1.KubeletServingSignerName, validRequest),
				newCSR("csr-approved", "system:node:node1", certificatesv1.KubeletServingSignerName, validRequest,
					certificatesv1.CertificateSigningRequestCondition{Type: certificatesv1.CertificateApproved},
				),
				newCSR("csr-bad-san", "system:node:node1", certificatesv1.KubeletServingSignerName,
					generateServingCSRPEM(t, []string{"evil.host"}, nil)),
			},
			wantApprovals: 1,
			validate: func(t *testing.T, fc *fake.Clientset) {
				updated := approvedCSR(fc)
				require.NotNil(t, updated)
				assert.Equal(t, "csr-valid", updated.Name)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := fake.NewClientset(tt.objects...)
			approver := NewCSRAutoApprover(fc)

			err := approver.approveKubeletServingCSRs(context.Background())

			require.NoError(t, err)
			assert.Equal(t, tt.wantApprovals, countApprovals(fc))
			tt.validate(t, fc)
		})
	}
}
