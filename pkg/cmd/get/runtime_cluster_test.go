package get

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteKubeconfigFile(t *testing.T) {
	t.Run("writes the kubeconfig data with 0600 permissions", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "my-cluster-kubeconfig.yaml")
		data := []byte("apiVersion: v1\nkind: Config\n")

		err := writeKubeconfigFile(path, data)
		require.NoError(t, err)

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, data, got)

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})

	t.Run("tightens permissions on a pre-existing, more loosely-permissioned file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "existing-kubeconfig.yaml")
		require.NoError(t, os.WriteFile(path, []byte("stale"), 0o644))

		data := []byte("apiVersion: v1\nkind: Config\n")
		err := writeKubeconfigFile(path, data)
		require.NoError(t, err)

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, data, got)

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})

	t.Run("returns a clear error when the destination directory doesn't exist", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "does-not-exist", "my-cluster-kubeconfig.yaml")

		err := writeKubeconfigFile(path, []byte("irrelevant"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to write kubeconfig to")
		assert.Contains(t, err.Error(), path)
	})
}
