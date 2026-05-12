//go:build linux

package component

import (
	"context"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/containerd/containerd"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	log "github.com/sirupsen/logrus"
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
	binaries := []struct{ src, dst string }{
		{"worker/containerd", path.Join(c.wrkCtx.BinDir, "containerd")},
		{"worker/containerd-shim-runc-v2", path.Join(c.wrkCtx.BinDir, "containerd-shim-runc-v2")},
		{"worker/runc", path.Join(c.wrkCtx.BinDir, "runc")},
		// #TODO: remove this
		{"worker/crictl", path.Join(c.wrkCtx.BinDir, "crictl")},
	}
	for _, b := range binaries {
		log.WithField("dst", b.dst).Info("extracting binary")
		if err := extractStreamed(b.src, b.dst); err != nil {
			return fmt.Errorf("failed to extract %s: %w", b.src, err)
		}
	}
	criPluginConfig := criconfig.DefaultConfig()
	if criPluginConfig.ContainerdConfig.Runtimes == nil {
		criPluginConfig.ContainerdConfig.Runtimes = make(map[string]criconfig.Runtime)
	}
	criPluginConfig.ContainerdConfig.Runtimes["runc"] = criconfig.Runtime{
		Type: "io.containerd.runc.v2",
		Options: map[string]any{
			"SystemdCgroup": true,
		},
	}
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

func (c *Containerd) Run(ctx context.Context) error {
	c.component = &procmgr.Component{
		Name:        "containerd",
		BinPath:     path.Join(c.wrkCtx.BinDir, "containerd"),
		LogLevel:    c.wrkCtx.LogLevel,
		LogFilePath: c.wrkCtx.ContainerdLogFile,
		Args: []string{
			"--state=" + c.wrkCtx.ContainerdState,
			"--root=" + c.wrkCtx.ContainerdRoot,
			"--address=" + c.wrkCtx.ContainerdAddress,
			"--config=" + c.wrkCtx.ContainerdConfig,
		},
		// Prepend BinDir to PATH so containerd can resolve containerd-shim-runc-v2
		// and runc without requiring them to be in the system PATH.
		Env: []string{
			fmt.Sprintf("PATH=%s:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", c.wrkCtx.BinDir),
		},
		MaxRetries:     5,
		InitialBackoff: time.Second,
		MaxBackoff:     30 * time.Second,
		StopTimeout:    10 * time.Second,
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	if err := c.component.Run(runCtx); err != nil {
		log.WithField("component", "containerd").WithError(err).Error("containerd exited")
	}
	return nil
}

func (c *Containerd) Teardown(ctx context.Context) error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.component == nil {
		return nil
	}
	return c.component.Teardown(ctx)
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
