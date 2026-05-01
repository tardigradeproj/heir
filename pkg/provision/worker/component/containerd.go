package component

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	"github.com/coreos/go-systemd/v22/unit"
	log "github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/systemd"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
)

type Containerd struct {
	wrkCtx typ.WorkerContext
}

func NewContainerd(wrkCtx typ.WorkerContext) *Containerd {
	return &Containerd{wrkCtx: wrkCtx}
}
func (c *Containerd) Setup() error {
	binaries := []struct{ src, dst string }{
		{"worker/containerd", path.Join(c.wrkCtx.BinDir, "containerd")},
		{"worker/containerd-shim-runc-v2", path.Join(c.wrkCtx.BinDir, "containerd-shim-runc-v2")},
		{"worker/runc", path.Join(c.wrkCtx.BinDir, "runc")},
	}
	for _, b := range binaries {
		log.WithField("dst", b.dst).Info("extracting binary")
		if err := extractStreamed(b.src, b.dst); err != nil {
			return fmt.Errorf("failed to extract %s: %w", b.src, err)
		}
	}
	criPluginConfig := criconfig.DefaultConfig()
	// Set pause image
	// #TOOD: sandboxContainerImage
	// criPluginConfig.SandboxImage = "custom-image"
	containerdConf, err := toml.Marshal(map[string]any{
		"version": 2,
		"plugins": map[string]any{
			"io.containerd.grpc.v1.cri": criPluginConfig,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal cri config: %w", err)
	}
	if err := os.WriteFile(c.wrkCtx.ContainerdConfig, containerdConf, 0644); err != nil {
		return fmt.Errorf("failed to write containerd config: %w", err)
	}
	return nil
}

const containerdUnit = "containerd.service"

func (c *Containerd) Run(ctx context.Context) error {

	sys, err := systemd.NewRunner(ctx, containerdUnit)
	if err != nil {
		log.WithError(err).Error("failed to establish systemd connection")
		return err
	}
	defer sys.Close()
	// Write the unit file. Overwriting on every call is safe and keeps the
	// configuration in sync with the current WorkerContext values.
	containerdBin := path.Join(c.wrkCtx.BinDir, "containerd")
	execStart := fmt.Sprintf(
		"%s --state=%s --root=%s --address=%s --config=%s",
		containerdBin,
		c.wrkCtx.ContainerdState,
		c.wrkCtx.ContainerdRoot,
		c.wrkCtx.ContainerdAddress,
		c.wrkCtx.ContainerdConfig,
	)
	// Prepend BinDir to PATH so containerd can resolve containerd-shim-runc-v2
	// and runc without requiring them to be in the system PATH.
	envPath := fmt.Sprintf("PATH=%s:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", c.wrkCtx.BinDir)

	unitOptions := []*unit.UnitOption{
		{Section: "Unit", Name: "Description", Value: "containerd container runtime"},
		{Section: "Unit", Name: "Documentation", Value: "https://containerd.io"},
		{Section: "Unit", Name: "After", Value: "network.target"},
		{Section: "Service", Name: "Environment", Value: envPath},
		{Section: "Service", Name: "ExecStart", Value: execStart},
		{Section: "Service", Name: "Restart", Value: "always"},
		{Section: "Service", Name: "RestartSec", Value: "5"},
		{Section: "Install", Name: "WantedBy", Value: "multi-user.target"},
	}
	if err := sys.SaveUnit(unitOptions); err != nil {
		log.WithError(err).Error("failed to write containerd unit")
		return err
	}

	if err := sys.Run(ctx); err != nil {
		return fmt.Errorf("failed to setup systemd daemon: %w", err)
	}

	// Wait until the containerd socket is accepting connections.
	waitCtx, cancel := context.WithTimeout(ctx, c.wrkCtx.ContainerdStartupTimeout)
	defer cancel()
	log.WithField("address", c.wrkCtx.ContainerdAddress).Info("waiting for containerd socket")
	return waitForContainerdSocket(waitCtx, c.wrkCtx.ContainerdAddress)
}

// Cleanup stops and disables the containerd service, removes the unit file
// via the systemd runner, then removes every file and directory that was
// created by Setup and Run.
// All errors are collected and returned together via errors.Join.
func (c *Containerd) Cleanup(ctx context.Context) error {
	var errs []error

	sys, err := systemd.NewRunner(ctx, containerdUnit)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to establish systemd connection: %w", err))
	} else {
		defer sys.Close()
		if err := sys.Cleanup(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup containerd systemd unit: %w", err))
		}
	}

	// Remove the binaries extracted by Setup.
	for _, name := range []string{"containerd", "containerd-shim-runc-v2", "runc"} {
		p := path.Join(c.wrkCtx.BinDir, name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("failed to remove binary %s: %w", p, err))
		} else {
			log.WithField("path", p).Info("binary removed")
		}
	}

	// Remove the containerd config directory (contains config.toml).
	configDir := path.Dir(c.wrkCtx.ContainerdConfig)
	if err := os.RemoveAll(configDir); err != nil {
		errs = append(errs, fmt.Errorf("failed to remove containerd config dir %s: %w", configDir, err))
	} else {
		log.WithField("path", configDir).Info("containerd config dir removed")
	}

	// Remove the state directory created by Run and populated by containerd.
	if err := os.RemoveAll(c.wrkCtx.ContainerdState); err != nil {
		errs = append(errs, fmt.Errorf("failed to remove containerd state dir %s: %w", c.wrkCtx.ContainerdState, err))
	} else {
		log.WithField("path", c.wrkCtx.ContainerdState).Info("containerd state dir removed")
	}

	// Remove the root directory (image layers, snapshots, metadata).
	if err := os.RemoveAll(c.wrkCtx.ContainerdRoot); err != nil {
		errs = append(errs, fmt.Errorf("failed to remove containerd root dir %s: %w", c.wrkCtx.ContainerdRoot, err))
	} else {
		log.WithField("path", c.wrkCtx.ContainerdRoot).Info("containerd root dir removed")
	}

	// Remove the socket file if containerd left it behind.
	if err := os.Remove(c.wrkCtx.ContainerdAddress); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("failed to remove containerd socket %s: %w", c.wrkCtx.ContainerdAddress, err))
	} else {
		log.WithField("path", c.wrkCtx.ContainerdAddress).Info("containerd socket removed")
	}

	return errors.Join(errs...)
}

func (c *Containerd) Teardown(ctx context.Context) error {
	sys, err := systemd.NewRunner(ctx, containerdUnit)
	if err != nil {
		log.WithError(err).Error("failed to establish systemd connection")
		return err
	}
	defer sys.Close()
	if err := sys.Stop(ctx); err != nil {
		log.WithError(err).Error("failed to stop systemd daemon")
		return fmt.Errorf("failed to stop containerd systemd daemon: %w", err)
	}
	return nil
}

// waitForContainerdSocket polls the containerd unix socket until it accepts a
// connection or ctx is cancelled.
func waitForContainerdSocket(ctx context.Context, address string) error {
	lg := log.WithField("address", address)
	client, err := containerd.New(address)
	if err != nil {
		return fmt.Errorf("failed to connect to containerd: %v", err)
	}
	defer client.Close()
	serving, err := client.IsServing(ctx)
	if err != nil {
		return fmt.Errorf("containerd health check failed: %v", err)
	}
	if serving {
		lg.Info("containerd is running and healthy")

		// fetch the version to prove we can communicate properly
		version, err := client.Version(ctx)
		if err == nil {
			log.WithField("version", version).
				WithField("revision", version.Revision).
				Info("containerd is running and healthy")
		} else {
			log.Info("could not fetch containerd version details")
		}
	} else {
		return fmt.Errorf("containerd socket exists, but the daemon is not currently serving")
	}
	return nil
}
