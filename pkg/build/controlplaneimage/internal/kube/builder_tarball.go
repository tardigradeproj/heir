package kube

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"sigs.k8s.io/kind/pkg/errors"
)

// directoryBuilder implements Bits for a local docker-ized make / bash build
type directoryBuilder struct {
	tarballPath string
}

var _ Builder = &directoryBuilder{}

// NewTarballBuilder returns a new Bits backed by the docker-ized build,
// given kubeRoot, the path to the kubernetes source directory
func NewTarballBuilder(tarballPath string) (Builder, error) {
	return &directoryBuilder{
		tarballPath: tarballPath,
	}, nil
}

// Build implements Bits.Build
func (b *directoryBuilder) Build() (Bits, error) {
	tmpDir, err := os.MkdirTemp(os.TempDir(), "k8s-tar-extract-")
	if err != nil {
		return nil, fmt.Errorf("error creating temporary directory for tar extraction: %w", err)
	}

	log.Infof("Extracting %q", b.tarballPath)
	err = extractTarball(b.tarballPath, tmpDir)
	if err != nil {
		return nil, fmt.Errorf("error extracting tar file: %w", err)
	}

	binDir := filepath.Join(tmpDir, "kubernetes/server/bin")
	contents, err := os.ReadFile(filepath.Join(tmpDir, "kubernetes/version"))
	if err != nil {
		return nil, errors.Wrap(err, "failed to get version")
	}
	sourceVersionRaw := strings.TrimSpace(string(contents))
	return &bits{
		binaryPaths: []string{
			filepath.Join(binDir, "kubeadm"),
			filepath.Join(binDir, "kubectl"),
			filepath.Join(binDir, "kube-apiserver"),
			filepath.Join(binDir, "kube-controller-manager"),
			filepath.Join(binDir, "kube-scheduler"),
		},
		imagePaths: []string{},
		version:    sourceVersionRaw,
	}, nil
}
