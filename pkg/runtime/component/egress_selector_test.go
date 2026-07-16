package component

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sigsyaml "sigs.k8s.io/yaml"
)

// egressSelectorDoc mirrors the apiserver.k8s.io/v1beta1 EgressSelectorConfiguration
// structure for test unmarshalling only.
type egressSelectorDoc struct {
	APIVersion       string            `yaml:"apiVersion"`
	Kind             string            `yaml:"kind"`
	EgressSelections []egressSelection `yaml:"egressSelections"`
}

type egressSelection struct {
	Name       string     `yaml:"name"`
	Connection connection `yaml:"connection"`
}

type connection struct {
	ProxyProtocol string    `yaml:"proxyProtocol"`
	Transport     transport `yaml:"transport"`
}

type transport struct {
	TCP tcpTransport `yaml:"tcp"`
}

type tcpTransport struct {
	URL       string    `yaml:"url"`
	TLSConfig tlsConfig `yaml:"tlsConfig"`
}

type tlsConfig struct {
	CABundle   string `yaml:"caBundle"`
	ClientCert string `yaml:"clientCert"`
	ClientKey  string `yaml:"clientKey"`
}

func baseEgressSelectorConfig() EgressSelectorConfig {
	return EgressSelectorConfig{
		EgressURL:      "https://tunnel-server-mycluster-egress.default.svc.cluster.local:9443",
		CACertPath:     "/etc/kubernetes/pki/ca.crt",
		ClientCertPath: "/etc/kubernetes/pki/apiserver-plane-tunnel.crt",
		ClientKeyPath:  "/etc/kubernetes/pki/apiserver-plane-tunnel.key",
	}
}

func TestCreateEgressSelectorConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		cfg      EgressSelectorConfig
		validate func(t *testing.T, doc egressSelectorDoc)
	}{
		{
			name: "apiVersion and kind are correct",
			cfg:  baseEgressSelectorConfig(),
			validate: func(t *testing.T, doc egressSelectorDoc) {
				assert.Equal(t, "apiserver.k8s.io/v1beta1", doc.APIVersion)
				assert.Equal(t, "EgressSelectorConfiguration", doc.Kind)
			},
		},
		{
			name: "exactly one egress selection named cluster",
			cfg:  baseEgressSelectorConfig(),
			validate: func(t *testing.T, doc egressSelectorDoc) {
				require.Len(t, doc.EgressSelections, 1)
				assert.Equal(t, "cluster", doc.EgressSelections[0].Name)
			},
		},
		{
			name: "proxy protocol is HTTPConnect",
			cfg:  baseEgressSelectorConfig(),
			validate: func(t *testing.T, doc egressSelectorDoc) {
				require.Len(t, doc.EgressSelections, 1)
				assert.Equal(t, "HTTPConnect", doc.EgressSelections[0].Connection.ProxyProtocol)
			},
		},
		{
			name: "egress URL is propagated to tcp transport",
			cfg: EgressSelectorConfig{
				EgressURL:      "https://tunnel-server-foo-egress.prod.svc.cluster.local:9443",
				CACertPath:     "/etc/kubernetes/pki/ca.crt",
				ClientCertPath: "/etc/kubernetes/pki/apiserver-plane-tunnel.crt",
				ClientKeyPath:  "/etc/kubernetes/pki/apiserver-plane-tunnel.key",
			},
			validate: func(t *testing.T, doc egressSelectorDoc) {
				require.Len(t, doc.EgressSelections, 1)
				assert.Equal(t, "https://tunnel-server-foo-egress.prod.svc.cluster.local:9443",
					doc.EgressSelections[0].Connection.Transport.TCP.URL)
			},
		},
		{
			name: "CA bundle path is set in TLS config",
			cfg: EgressSelectorConfig{
				EgressURL:      "https://tunnel-server-mycluster-egress.default.svc.cluster.local:9443",
				CACertPath:     "/custom/pki/ca.crt",
				ClientCertPath: "/etc/kubernetes/pki/apiserver-plane-tunnel.crt",
				ClientKeyPath:  "/etc/kubernetes/pki/apiserver-plane-tunnel.key",
			},
			validate: func(t *testing.T, doc egressSelectorDoc) {
				require.Len(t, doc.EgressSelections, 1)
				assert.Equal(t, "/custom/pki/ca.crt",
					doc.EgressSelections[0].Connection.Transport.TCP.TLSConfig.CABundle)
			},
		},
		{
			name: "client cert path is set in TLS config",
			cfg: EgressSelectorConfig{
				EgressURL:      "https://tunnel-server-mycluster-egress.default.svc.cluster.local:9443",
				CACertPath:     "/etc/kubernetes/pki/ca.crt",
				ClientCertPath: "/custom/pki/apiserver-plane-tunnel.crt",
				ClientKeyPath:  "/etc/kubernetes/pki/apiserver-plane-tunnel.key",
			},
			validate: func(t *testing.T, doc egressSelectorDoc) {
				require.Len(t, doc.EgressSelections, 1)
				assert.Equal(t, "/custom/pki/apiserver-plane-tunnel.crt",
					doc.EgressSelections[0].Connection.Transport.TCP.TLSConfig.ClientCert)
			},
		},
		{
			name: "client key path is set in TLS config",
			cfg: EgressSelectorConfig{
				EgressURL:      "https://tunnel-server-mycluster-egress.default.svc.cluster.local:9443",
				CACertPath:     "/etc/kubernetes/pki/ca.crt",
				ClientCertPath: "/etc/kubernetes/pki/apiserver-plane-tunnel.crt",
				ClientKeyPath:  "/custom/pki/apiserver-plane-tunnel.key",
			},
			validate: func(t *testing.T, doc egressSelectorDoc) {
				require.Len(t, doc.EgressSelections, 1)
				assert.Equal(t, "/custom/pki/apiserver-plane-tunnel.key",
					doc.EgressSelections[0].Connection.Transport.TCP.TLSConfig.ClientKey)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := CreateEgressSelectorConfiguration(tt.cfg)
			require.NoError(t, err)
			require.NotEmpty(t, out)

			var doc egressSelectorDoc
			require.NoError(t, sigsyaml.Unmarshal(out, &doc))
			tt.validate(t, doc)
		})
	}
}
