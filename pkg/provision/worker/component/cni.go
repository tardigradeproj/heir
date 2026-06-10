package component

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
)

type Cni struct {
	wrkCtx *typ.WorkerContext
}

func NewCni(wrkCtx *typ.WorkerContext) *Cni {
	return &Cni{wrkCtx: wrkCtx}
}

func (c *Cni) Setup() error {
	log.WithField("dst", c.wrkCtx.CNIBinFolderPath).Info("extracting CNI binaries")
	if err := extractTarZst("worker/cni.tar.zst", c.wrkCtx.CNIBinFolderPath); err != nil {
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
