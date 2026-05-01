package worker

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/coreos/go-systemd/v22/unit"
	log "github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
)

type systemdService struct {
	config func(execStartFlags map[string]string) []*unit.UnitOption
}

func services() map[string]systemdService {
	return map[string]systemdService{
		"containerd.service": {
			config: func(_ map[string]string) []*unit.UnitOption {
				return []*unit.UnitOption{
					{Section: "Unit", Name: "Description", Value: "containerd container runtime"},
					{Section: "Unit", Name: "Documentation", Value: "https://containerd.io"},
					{Section: "Unit", Name: "After", Value: "network.target"},
					{Section: "Service", Name: "ExecStart", Value: fmt.Sprintf("%s --config=%s", containerdBin, containerdConfiguration)},
					{Section: "Service", Name: "Restart", Value: "always"},
					{Section: "Service", Name: "RestartSec", Value: "5"},
					{Section: "Install", Name: "WantedBy", Value: "multi-user.target"},
				}
			},
		},
		"kubelet.service": {
			config: func(execStartFlags map[string]string) []*unit.UnitOption {
				var sb strings.Builder
				sb.WriteString(kubeletBin)
				for k, v := range execStartFlags {
					sb.WriteString(fmt.Sprintf(" --%s=%s", k, v))
				}
				return []*unit.UnitOption{
					{Section: "Unit", Name: "Description", Value: "Kubelet"},
					{Section: "Unit", Name: "After", Value: "network.target containerd.service"},
					{Section: "Unit", Name: "Requires", Value: "containerd.service"},
					{Section: "Service", Name: "ExecStart", Value: sb.String()},
					{Section: "Service", Name: "Restart", Value: "always"},
					{Section: "Service", Name: "RestartSec", Value: "5"},
					{Section: "Install", Name: "WantedBy", Value: "multi-user.target"},
				}
			},
		},
	}
}

// setupUnits writes the unit files for containerd and kubelet, reloads the
// procmgr daemon, enables both units, then starts containerd followed by kubelet.
func setupUnits(ctx context.Context, jctx *typ.WorkerContext) error {
	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to procmgr: %w", err)
	}
	defer conn.Close()

	kubeletArgs := jctx.KubeletExtraArgs
	kubeletArgs["config"] = kubeletConfigFile
	kubeletArgs["bootstrap-kubeconfig"] = kubeletBootstrapKubeconfig
	kubeletArgs["cert-dir"] = kubeletCertDir
	kubeletArgs["kubeconfig"] = kubeletKubeconfig

	unitFlags := map[string]map[string]string{
		"containerd.service": {
			"config": containerdConfiguration,
		},
		"kubelet.service": kubeletArgs,
	}

	for name, svc := range services() {
		log.WithField("unit", name).Info("writing unit file")
		content, err := io.ReadAll(unit.Serialize(svc.config(unitFlags[name])))
		if err != nil {
			return fmt.Errorf("failed to serialize %s: %w", name, err)
		}
		if err := os.WriteFile("/etc/procmgr/system/"+name, content, 0644); err != nil {
			return fmt.Errorf("failed to write %s unit file: %w", name, err)
		}
	}

	log.Info("reloading procmgr daemon")
	if err := conn.ReloadContext(ctx); err != nil {
		return fmt.Errorf("failed to reload procmgr daemon: %w", err)
	}

	unitNames := []string{"containerd.service", "kubelet.service"}

	log.WithField("units", unitNames).Info("enabling units")
	if _, _, err := conn.EnableUnitFilesContext(ctx, unitNames, false, true); err != nil {
		return fmt.Errorf("failed to enable units: %w", err)
	}

	// Start containerd before kubelet since kubelet depends on it.
	for _, name := range unitNames {
		log.WithField("unit", name).Info("starting unit")
		ch := make(chan string, 1)
		if _, err := conn.StartUnitContext(ctx, name, "replace", ch); err != nil {
			return fmt.Errorf("failed to start %s: %w", name, err)
		}
		if result := <-ch; result != "done" {
			return fmt.Errorf("failed to start %s: job result %q", name, result)
		}
		log.WithField("unit", name).Info("unit started")
	}

	return nil
}
