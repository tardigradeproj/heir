package component

import (
	"context"

	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/procmgr"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
)

type Containerd struct {
	wrkCtx    *typ.WorkerContext
	component *procmgr.Component
	cancel    context.CancelFunc
}

func NewContainerd(wrkCtx *typ.WorkerContext) *Containerd {
	return &Containerd{wrkCtx: wrkCtx}
}

func (c *Containerd) Setup() error {
	return nil
}

func (c *Containerd) Run(ctx context.Context) error {

	return nil
}

func (c *Containerd) Teardown(ctx context.Context) error {
	return nil
}
