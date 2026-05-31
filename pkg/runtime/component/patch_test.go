package component

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sigsyaml "sigs.k8s.io/yaml"
)

func TestApplyKubeletConfigPatch(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		patch    string
		wantErr  bool
		validate func(t *testing.T, result map[string]interface{})
	}{
		{
			name: "overrides a top-level field",
			base: `
cgroupDriver: cgroupfs
failSwapOn: true
`,
			patch: `
cgroupDriver: systemd
`,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, "systemd", result["cgroupDriver"])
				assert.Equal(t, true, result["failSwapOn"])
			},
		},
		{
			name: "adds a field absent from the base",
			base: `
cgroupDriver: systemd
`,
			patch: `
staticPodPath: /etc/heir/manifests
`,
			validate: func(t *testing.T, result map[string]interface{}) {
				assert.Equal(t, "systemd", result["cgroupDriver"])
				assert.Equal(t, "/etc/heir/manifests", result["staticPodPath"])
			},
		},
		{
			name: "merges nested objects without dropping unpatched sibling fields",
			base: `
authentication:
  anonymous:
    enabled: false
  x509:
    clientCAFile: /etc/pki/ca.crt
`,
			patch: `
authentication:
  anonymous:
    enabled: true
`,
			validate: func(t *testing.T, result map[string]interface{}) {
				auth, _ := result["authentication"].(map[string]interface{})
				anon, _ := auth["anonymous"].(map[string]interface{})
				assert.Equal(t, true, anon["enabled"])
				x509, _ := auth["x509"].(map[string]interface{})
				assert.Equal(t, "/etc/pki/ca.crt", x509["clientCAFile"])
			},
		},
		{
			name: "replaces an array field entirely",
			base: `
clusterDNS:
- 10.96.0.10
`,
			patch: `
clusterDNS:
- 192.168.1.10
- 192.168.1.11
`,
			validate: func(t *testing.T, result map[string]interface{}) {
				dns, _ := result["clusterDNS"].([]interface{})
				require.Len(t, dns, 2)
				assert.Equal(t, "192.168.1.10", dns[0])
				assert.Equal(t, "192.168.1.11", dns[1])
			},
		},
		{
			name:    "returns an error on invalid base YAML",
			base:    ":\tinvalid: [yaml",
			patch:   "cgroupDriver: systemd",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := configPatch([]byte(tt.base), tt.patch)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			var parsed map[string]interface{}
			require.NoError(t, sigsyaml.Unmarshal(result, &parsed))

			tt.validate(t, parsed)
		})
	}
}
