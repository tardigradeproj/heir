package component

import (
	"bytes"
	"fmt"

	"github.com/tardigradeproj/heir/pkg/templatewriter"
)

// EgressSelectorConfig holds the parameters needed to render an
// EgressSelectorConfiguration for the kube-apiserver.
type EgressSelectorConfig struct {
	// EgressURL is the HTTPS address of the plane-tunnel egress-selector service,
	// e.g. https://tunnel-server-mycluster-egress.default.svc.cluster.local:9443
	EgressURL      string
	CACertPath     string
	ClientCertPath string
	ClientKeyPath  string
}

// CreateEgressSelectorConfiguration renders the apiserver.k8s.io/v1beta1
// EgressSelectorConfiguration that routes the API server's kubelet traffic
// through the plane tunnel egress-selector.
func CreateEgressSelectorConfiguration(cfg EgressSelectorConfig) ([]byte, error) {
	var buf bytes.Buffer
	if err := (&templatewriter.TemplateWriter{
		Name:     "egress-selector",
		Template: egressSelectorTemplate,
		Data:     cfg,
	}).WriteToBuffer(&buf); err != nil {
		return nil, fmt.Errorf("failed to write egress selector configuration: %w", err)
	}
	return buf.Bytes(), nil
}

const egressSelectorTemplate = `apiVersion: apiserver.k8s.io/v1beta1
kind: EgressSelectorConfiguration
egressSelections:
- name: cluster
  connection:
    proxyProtocol: HTTPConnect
    transport:
      tcp:
        url: {{ .EgressURL }}
        tlsConfig:
          caBundle: {{ .CACertPath }}
          clientCert: {{ .ClientCertPath }}
          clientKey: {{ .ClientKeyPath }}
`
