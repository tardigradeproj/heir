package component

import (
	"context"
	"fmt"
	"io/fs"

	log "github.com/sirupsen/logrus"
	artifact "github.com/tardigradeproj/heir/artifacts"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
)

type Cni struct {
	wrkCtx *typ.WorkerContext
	fsys   fs.FS
}

func NewCni(wrkCtx *typ.WorkerContext) *Cni {
	return &Cni{wrkCtx: wrkCtx, fsys: artifact.FS}
}

func (c *Cni) Setup() error {
	log.WithField("dst", c.wrkCtx.CNIBinFolderPath).Info("extracting CNI binaries")
	if err := extractTarZstFrom(c.fsys, "worker/cni.tar.zst", c.wrkCtx.CNIBinFolderPath); err != nil {
		return fmt.Errorf("failed to extract CNI binaries: %w", err)
	}
	return nil
}

func (c *Cni) Run(_ context.Context) error {
	return nil
}

func (c *Cni) Teardown(_ context.Context) error {
	return nil
}
