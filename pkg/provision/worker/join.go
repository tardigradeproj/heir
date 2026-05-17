package worker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/coreos/go-systemd/v22/dbus"
	sdunit "github.com/coreos/go-systemd/v22/unit"
	"github.com/sirupsen/logrus"
	btsp "github.com/tardigrade-runtime/samaritano/pkg/provision/worker/bootstrap"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
)

const (
	installPath = "/usr/local/bin/samaritano"
	unitName    = "samaritano.service"
	unitPath    = "/etc/systemd/system/samaritano.service"
)

func Join(ctx context.Context, token string, opts ...typ.Option) error {
	workerCtx := typ.NewWorkerContextWithDefaults()
	workerCtx.Token = token
	for _, opt := range opts {
		opt(workerCtx)
	}
	log := logrus.WithField("operation", "join")
	log.Debug("creating required directories")
	if err := createDirectories(workerCtx); err != nil {
		return fmt.Errorf("failed to create config directories: %w", err)
	}
	log.Info("saving bootstrap kubeconfig")
	if err := btsp.SaveBootstrapKubeconfig(token, workerCtx.KubeletBootstrapKubeconfigPath, workerCtx.KubeletPKICaCertPath); err != nil {
		return fmt.Errorf("failed to save bootstrap kubeconfig: %w", err)
	}

	log.WithField("dst", installPath).Info("installing binary")
	if err := installSelf(installPath); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	log.WithField("unit", unitName).Info("registering systemd unit")
	if err := installSystemdUnit(ctx, workerCtx); err != nil {
		return fmt.Errorf("failed to register systemd unit: %w", err)
	}

	return nil
}

func installSelf(dst string) error {
	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine executable path: %w", err)
	}
	src, err = filepath.EvalSymlinks(src)
	if err != nil {
		return fmt.Errorf("could not resolve executable symlink: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("could not create install directory: %w", err)
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func installSystemdUnit(ctx context.Context, workerCtx *typ.WorkerContext) error {
	execStart := installPath + " worker"
	if extra := serializeExtraArgs(workerCtx.KubeletExtraArgs); extra != "" {
		execStart += " --kubelet-extra-args=" + extra
	}

	opts := []*sdunit.UnitOption{
		sdunit.NewUnitOption("Unit", "Description", "Samaritano Worker Node Agent"),
		sdunit.NewUnitOption("Unit", "After", "network-online.target"),
		sdunit.NewUnitOption("Unit", "Wants", "network-online.target"),
		sdunit.NewUnitOption("Service", "ExecStart", execStart),
		sdunit.NewUnitOption("Service", "Restart", "always"),
		sdunit.NewUnitOption("Service", "RestartSec", "5"),
		sdunit.NewUnitOption("Install", "WantedBy", "multi-user.target"),
	}

	unitReader := sdunit.Serialize(opts)
	unitContent, err := io.ReadAll(unitReader)
	if err != nil {
		return fmt.Errorf("failed to serialize unit file: %w", err)
	}
	if err := os.WriteFile(unitPath, unitContent, 0644); err != nil {
		return fmt.Errorf("failed to write unit file: %w", err)
	}

	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to systemd: %w", err)
	}
	defer conn.Close()

	if err := conn.ReloadContext(ctx); err != nil {
		return fmt.Errorf("systemd daemon-reload failed: %w", err)
	}

	if _, _, err := conn.EnableUnitFilesContext(ctx, []string{unitPath}, false, true); err != nil {
		return fmt.Errorf("failed to enable unit %q: %w", unitName, err)
	}

	resultCh := make(chan string, 1)
	if _, err := conn.StartUnitContext(ctx, unitName, "replace", resultCh); err != nil {
		return fmt.Errorf("failed to start unit %q: %w", unitName, err)
	}
	if result := <-resultCh; result != "done" {
		return fmt.Errorf("unit %q start job finished with result %q", unitName, result)
	}

	return nil
}

func serializeExtraArgs(args map[string]string) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for k, v := range args {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}
