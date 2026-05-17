package component

import (
	"context"
	"fmt"
	"os"
	"path"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/procmgr"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
)

type Kubelet struct {
	wrkCtx      *typ.WorkerContext
	component   *procmgr.Component
	cancel      context.CancelFunc
	nodeProfile *typ.NodeProfile
	hostname    string
}

func NewKubelet(wrkCtx *typ.WorkerContext, nodeProfile *typ.NodeProfile, hostname string) *Kubelet {
	return &Kubelet{wrkCtx: wrkCtx, nodeProfile: nodeProfile, hostname: hostname}
}

func (k *Kubelet) Setup() error {
	binaries := []struct{ src, dst string }{
		{"worker/kubelet", path.Join(k.wrkCtx.BinDir, "kubelet")},
	}
	for _, b := range binaries {
		log.WithField("dst", b.dst).Info("extracting binary")
		if err := extractStreamed(b.src, b.dst); err != nil {
			return fmt.Errorf("failed to extract %s: %w", b.src, err)
		}
	}

	// saving kubeletConfig
	if err := os.WriteFile(k.wrkCtx.KubeletConfigFile, []byte(k.nodeProfile.KubeletConfiguration), 0644); err != nil {
		return fmt.Errorf("failed to write containerd config: %w", err)
	}

	return nil
}

// --kubeconfig=/run/k0s/kubelet-direct.conf --v=1 --cert-dir=/var/lib/k0s/kubelet/pki
//--runtime-cgroups=/system.slice/containerd.service --hostname-override=lab --node-labels=node.k0sproject.io/role=control-plane
//--containerd=/run/k0s/containerd.sock --root-dir=/var/lib/k0s/kubelet --config=/run/k0s/kubelet/config.yaml

//  /usr/bin/kubelet --bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf --kubeconfig=/etc/kubernetes/kubelet.conf --config=/var/lib/kubelet/config.yaml
// --node-ip=172.19.0.2 --node-labels= --pod-infra-container-image=registry.k8s.io/pause:3.10.1
// --provider-id=kind://docker/kind/kind-control-plane --runtime-cgroups=/system.slice/containerd.service

func (k *Kubelet) Run(ctx context.Context) error {

	defaultArgs := map[string]string{
		"kubeconfig":      k.wrkCtx.KubeletKubeConfigPath,
		"config":          k.wrkCtx.KubeletConfigFile,
		"containerd":      k.wrkCtx.ContainerdAddress,
		"cert-dir":        k.wrkCtx.KubeletPKIPath,
		"runtime-cgroups": "/system.slice/containerd.service",
		"v":               "2",
	}
	// start with control plane KubeletExtraArgs since they don't have priority over CLI
	for k, v := range k.nodeProfile.KubeletExtraArgs {
		defaultArgs[k] = v
	}
	for k, v := range k.wrkCtx.KubeletExtraArgs {
		defaultArgs[k] = v
	}
	args := make([]string, 0, len(defaultArgs))
	for k, v := range defaultArgs {
		args = append(args, fmt.Sprintf("--%s=%s", k, v))
	}
	k.component = &procmgr.Component{
		Name:           "kubelet",
		BinPath:        path.Join(k.wrkCtx.BinDir, "kubelet"),
		LogLevel:       k.wrkCtx.LogLevel,
		LogFilePath:    k.wrkCtx.KubeletLogFile,
		Args:           args,
		Env:            []string{},
		MaxRetries:     5,
		InitialBackoff: time.Second,
		MaxBackoff:     30 * time.Second,
		StopTimeout:    10 * time.Second,
	}
	runCtx, cancel := context.WithCancel(ctx)
	k.cancel = cancel
	if err := k.component.Run(runCtx); err != nil {
		return err
	}
	return nil
}
func (k *Kubelet) Teardown(ctx context.Context) error {
	if k.cancel != nil {
		k.cancel()
	}
	if k.component == nil {
		return nil
	}
	return k.component.Teardown(ctx)
}
