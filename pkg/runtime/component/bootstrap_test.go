package component

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

func TestCreateBootstrapManifest(t *testing.T) {
	tests := []struct {
		name     string
		validate func(t *testing.T, resources map[string][]byte)
	}{
		{
			name: "returns three ClusterRoleBindings",
			validate: func(t *testing.T, resources map[string][]byte) {
				for _, key := range []string{
					"ClusterRoleBinding/heir:kubelet-bootstrap",
					"ClusterRoleBinding/heir:kubelet-bootstrap-auto-approve-csrs",
					"ClusterRoleBinding/heir:kubelet-cert-renew",
				} {
					assert.Contains(t, resources, key, "missing resource %s", key)
				}
			},
		},
		{
			name: "kubelet-bootstrap binds system:node-bootstrapper for bootstrappers",
			validate: func(t *testing.T, resources map[string][]byte) {
				var crb rbacv1.ClusterRoleBinding
				require.NoError(t, sigsyaml.Unmarshal(resources["ClusterRoleBinding/heir:kubelet-bootstrap"], &crb))
				assert.Equal(t, "system:node-bootstrapper", crb.RoleRef.Name)
				assert.Equal(t, "ClusterRole", crb.RoleRef.Kind)
				require.Len(t, crb.Subjects, 1)
				assert.Equal(t, "Group", crb.Subjects[0].Kind)
				assert.Equal(t, "system:bootstrappers:worker", crb.Subjects[0].Name)
			},
		},
		{
			name: "kubelet-bootstrap-auto-approve-csrs binds nodeclient role for bootstrappers",
			validate: func(t *testing.T, resources map[string][]byte) {
				var crb rbacv1.ClusterRoleBinding
				require.NoError(t, sigsyaml.Unmarshal(resources["ClusterRoleBinding/heir:kubelet-bootstrap-auto-approve-csrs"], &crb))
				assert.Equal(t, "system:certificates.k8s.io:certificatesigningrequests:nodeclient", crb.RoleRef.Name)
				assert.Equal(t, "ClusterRole", crb.RoleRef.Kind)
				require.Len(t, crb.Subjects, 1)
				assert.Equal(t, "Group", crb.Subjects[0].Kind)
				assert.Equal(t, "system:bootstrappers:worker", crb.Subjects[0].Name)
			},
		},
		{
			name: "kubelet-cert-renew binds selfnodeclient role for system:nodes",
			validate: func(t *testing.T, resources map[string][]byte) {
				var crb rbacv1.ClusterRoleBinding
				require.NoError(t, sigsyaml.Unmarshal(resources["ClusterRoleBinding/heir:kubelet-cert-renew"], &crb))
				assert.Equal(t, "system:certificates.k8s.io:certificatesigningrequests:selfnodeclient", crb.RoleRef.Name)
				assert.Equal(t, "ClusterRole", crb.RoleRef.Kind)
				require.Len(t, crb.Subjects, 1)
				assert.Equal(t, "Group", crb.Subjects[0].Kind)
				assert.Equal(t, "system:nodes", crb.Subjects[0].Name)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := CreateBootstrapManifest()
			require.NotEmpty(t, manifest)
			resources := parseManifest(t, manifest)
			tt.validate(t, resources)
		})
	}
}
