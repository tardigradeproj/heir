package component

import (
	"context"
	"fmt"
	"path"

	log "github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
)

type Cni struct {
	wrkCtx *typ.WorkerContext
}

func NewCni(wrkCtx *typ.WorkerContext) *Cni {
	return &Cni{wrkCtx: wrkCtx}
}

func (c *Cni) Setup() error {
	binaries := []struct{ src, dst string }{
		{"worker/cni/bridge", path.Join(c.wrkCtx.CNIBinFolderPath, "bridge")},
		{"worker/cni/dhcp", path.Join(c.wrkCtx.CNIBinFolderPath, "dhcp")},
		{"worker/cni/dummy", path.Join(c.wrkCtx.CNIBinFolderPath, "dummy")},
		{"worker/cni/firewall", path.Join(c.wrkCtx.CNIBinFolderPath, "firewall")},
		{"worker/cni/host-device", path.Join(c.wrkCtx.CNIBinFolderPath, "host-device")},
		{"worker/cni/host-local", path.Join(c.wrkCtx.CNIBinFolderPath, "host-local")},
		{"worker/cni/ipvlan", path.Join(c.wrkCtx.CNIBinFolderPath, "ipvlan")},
		{"worker/cni/loopback", path.Join(c.wrkCtx.CNIBinFolderPath, "loopback")},
		{"worker/cni/macvlan", path.Join(c.wrkCtx.CNIBinFolderPath, "macvlan")},
		{"worker/cni/portmap", path.Join(c.wrkCtx.CNIBinFolderPath, "portmap")},
		{"worker/cni/ptp", path.Join(c.wrkCtx.CNIBinFolderPath, "ptp")},
		{"worker/cni/sbr", path.Join(c.wrkCtx.CNIBinFolderPath, "sbr")},
		{"worker/cni/static", path.Join(c.wrkCtx.CNIBinFolderPath, "static")},
		{"worker/cni/tap", path.Join(c.wrkCtx.CNIBinFolderPath, "tap")},
		{"worker/cni/tuning", path.Join(c.wrkCtx.CNIBinFolderPath, "tuning")},
		{"worker/cni/vlan", path.Join(c.wrkCtx.CNIBinFolderPath, "vlan")},
		{"worker/cni/vrf", path.Join(c.wrkCtx.CNIBinFolderPath, "vrf")},
	}
	for _, b := range binaries {
		log.WithField("dst", b.dst).Info("extracting binary")
		if err := extractStreamed(b.src, b.dst); err != nil {
			return fmt.Errorf("failed to extract %s: %w", b.src, err)
		}
	}
	return nil
}

func (c *Cni) Run(_ context.Context) error {
	return nil
}

func (c *Cni) Teardown(_ context.Context) error {
	return nil
}
