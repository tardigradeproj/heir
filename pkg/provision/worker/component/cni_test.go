package component

import (
	"archive/tar"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
)

// buildCNITarZst creates an in-memory zstd-compressed tar archive containing
// the given name→content pairs.
func buildCNITarZst(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	require.NoError(t, err)
	tw := tar.NewWriter(enc)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Size:     int64(len(content)),
			Mode:     0755,
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, enc.Close())
	return buf.Bytes()
}

// cniTestFS returns an fstest.MapFS with the archive at the arch-specific path
// that embedPath resolves to.
func cniTestFS(t *testing.T, files map[string][]byte) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		fmt.Sprintf("worker/%s/cni.tar.zst", runtime.GOARCH): {
			Data: buildCNITarZst(t, files),
		},
	}
}

func TestCni_Setup(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(t *testing.T) *Cni
		assertion func(t *testing.T, err error, dstDir string)
	}{
		{
			name: "extracts all files from archive to destination directory",
			prepare: func(t *testing.T) *Cni {
				dstDir := t.TempDir()
				return &Cni{
					wrkCtx: &typ.WorkerContext{CNIBinFolderPath: dstDir},
					fsys: cniTestFS(t, map[string][]byte{
						"bridge":   []byte("bridge-bin"),
						"loopback": []byte("loopback-bin"),
						"portmap":  []byte("portmap-bin"),
					}),
				}
			},
			assertion: func(t *testing.T, err error, dstDir string) {
				require.NoError(t, err)
				for _, name := range []string{"bridge", "loopback", "portmap"} {
					_, statErr := os.Stat(filepath.Join(dstDir, name))
					assert.NoError(t, statErr, "expected file %q to exist in destination", name)
				}
			},
		},
		{
			name: "extracted files are written with executable permissions",
			prepare: func(t *testing.T) *Cni {
				dstDir := t.TempDir()
				return &Cni{
					wrkCtx: &typ.WorkerContext{CNIBinFolderPath: dstDir},
					fsys:   cniTestFS(t, map[string][]byte{"bridge": []byte("bridge-bin")}),
				}
			},
			assertion: func(t *testing.T, err error, dstDir string) {
				require.NoError(t, err)
				info, statErr := os.Stat(filepath.Join(dstDir, "bridge"))
				require.NoError(t, statErr)
				assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
			},
		},
		{
			name: "skips files that already exist in the destination",
			prepare: func(t *testing.T) *Cni {
				dstDir := t.TempDir()
				require.NoError(t, os.WriteFile(
					filepath.Join(dstDir, "bridge"), []byte("original"), 0755,
				))
				return &Cni{
					wrkCtx: &typ.WorkerContext{CNIBinFolderPath: dstDir},
					fsys:   cniTestFS(t, map[string][]byte{"bridge": []byte("new-content")}),
				}
			},
			assertion: func(t *testing.T, err error, dstDir string) {
				require.NoError(t, err)
				content, readErr := os.ReadFile(filepath.Join(dstDir, "bridge"))
				require.NoError(t, readErr)
				assert.Equal(t, []byte("original"), content, "pre-existing file must not be overwritten")
			},
		},
		{
			name: "returns error when archive is absent from the filesystem",
			prepare: func(t *testing.T) *Cni {
				return &Cni{
					wrkCtx: &typ.WorkerContext{CNIBinFolderPath: t.TempDir()},
					fsys:   fstest.MapFS{},
				}
			},
			assertion: func(t *testing.T, err error, _ string) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "failed to extract CNI binaries")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cni := tt.prepare(t)
			err := cni.Setup()
			tt.assertion(t, err, cni.wrkCtx.CNIBinFolderPath)
		})
	}
}
