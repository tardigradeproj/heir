//go:build linux

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	btsp "github.com/tardigrade-runtime/samaritano/pkg/provision/worker/bootstrap"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/component"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/proxy"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/sys"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
	apimachineryruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
)

func Run(ctx context.Context, opts ...typ.Option) error {
	workerCtx := typ.NewWorkerContextWithDefaults()
	for _, opt := range opts {
		opt(workerCtx)
	}
	log := logrus.WithField("hostname", hostname)
	log.Info("starting worker node provisioning")

	log.Debug("creating required directories")
	if err := createDirectories(workerCtx); err != nil {
		return fmt.Errorf("failed to create config directories: %w", err)
	}

	log.Debug("configuring host system")
	if err := sys.Configure(); err != nil {
		return fmt.Errorf("failed to setup host: %w", err)
	}

	log.Debug("configuring API server local proxy")
	apiServerProxy := proxy.New([]string{})
	apiServerProxyCh := make(chan error)
	if err := registerApiServerExternalAddressOnLocalProxy(log, workerCtx, apiServerProxy); err != nil {
		return fmt.Errorf("failed to create base conditions to start local API server proxy: %w", err)
	}
	ctx, cancelApiServerProxy := context.WithCancel(ctx)
	go func() {
		log.WithField("api.server.address", workerCtx.ApiServerLocalAddress).Info("starting API server proxy")
		if err := apiServerProxy.Run(ctx); err != nil {
			log.WithError(err).Error("failed to start/run API server local TCP proxy")
			apiServerProxyCh <- err
			cancelApiServerProxy()
		}
	}()

	log.Debug("performing TLS bootstrap")
	if err := btsp.BootstrapKubeletClientConfig(ctx, workerCtx, hostname); err != nil {
		return fmt.Errorf("failed to perform TLS bootstrap: %w", err)
	}

	log.Debug("reading worker node profile")
	profile, err := btsp.ReadWorkerNodeProfile(ctx, workerCtx)
	if err != nil {
		return fmt.Errorf("failed to read worker node profile: %w", err)
	}

	log.Debug("updating API server local proxy target addresses")
	apiServerProxy.UpdateServers(profile.ApiServerExternalAddress)

	runners := []Runner{
		component.NewContainerd(workerCtx),
		component.NewKubelet(workerCtx, profile, hostname),
	}

	log.Debug("setting up components")
	for _, rn := range runners {
		if err := rn.Setup(); err != nil {
			return fmt.Errorf("failed to setup component: %w", err)
		}
	}

	log.Info("starting components")
	errCh := make(chan error, len(runners))
	for _, rn := range runners {
		go func(r Runner) {
			errCh <- r.Run(ctx)
		}(rn)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var runErr error
	select {
	case apiServerProxyErr := <-apiServerProxyCh:
		log.WithError(apiServerProxyErr).Error("API server proxy failure")
	case sig := <-sigCh:
		log.WithField("signal", sig).Info("received termination signal, tearing down")
	case runErr = <-errCh:
		log.WithError(runErr).Error("component exited with error, tearing down")
	case <-ctx.Done():
		log.Debug("context cancelled, tearing down")
	}

	log.Info("tearing down components")
	teardownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, rn := range runners {
		if err := rn.Teardown(teardownCtx); err != nil {
			log.WithError(err).Error("failed to teardown component")
		}
	}

	log.Info("worker node shut down")
	return runErr
}

// registerApiServerExternalAddressOnLocalProxy seeds the local API server proxy with upstream
// addresses before TLS bootstrap runs. This allows the proxy to forward traffic to the API
// server even on the very first startup when no node profile has been written yet.
//
// Resolution order:
//  1. If the node profile file (NodeProfileLocalFilePath) exists, load it and use the
//     apiServerExternalAddress list it contains.
//  2. If the node profile file is absent, fall back to the bootstrap kubeconfig
//     (KubeletBootstrapKubeconfigPath) and read the additionalApiServerProxyAddresses
//     extension from the current cluster entry.
func registerApiServerExternalAddressOnLocalProxy(log *logrus.Entry, workerContext *typ.WorkerContext, apiServerProxy *proxy.APIServerProxy) error {
	log.Info("registering API server external address on local proxy")

	_, err := os.Stat(workerContext.NodeProfileLocalFilePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to stat node profile file: %w", err)
		}

		// Node profile has not been written yet (pre-bootstrap). Read the
		// additionalApiServerProxyAddresses extension from the bootstrap kubeconfig so
		// the proxy has at least the addresses that were baked in at join time.
		log.Warn("node profile file does not exist, reading additional API server proxy addresses from bootstrap kubeconfig")
		addresses, err := readAdditionalApiServerProxyAddresses(workerContext.KubeletBootstrapKubeconfigPath)
		if err != nil {
			return fmt.Errorf("failed to read additionalApiServerProxyAddresses from bootstrap kubeconfig: %w", err)
		}
		if len(addresses) > 0 {
			log.WithField("external.address.ln", len(addresses)).Debug("updating external addresses on apiServer proxy from bootstrap kubeconfig")
			apiServerProxy.UpdateServers(addresses)
		}
		return nil
	}

	// Node profile exists — load it and apply the addresses it contains.
	nodeProfile := &typ.NodeProfile{}
	if err := nodeProfile.Load(workerContext.NodeProfileLocalFilePath); err != nil {
		return err
	}
	logrus.WithField("external.address.ln", len(nodeProfile.ApiServerExternalAddress)).Debug("updating external addresses on apiServer proxy")
	apiServerProxy.UpdateServers(nodeProfile.ApiServerExternalAddress)
	return nil
}

// readAdditionalApiServerProxyAddresses loads a kubeconfig file and returns the
// additionalApiServerProxyAddresses extension value from the current cluster entry.
// The extension is expected to be a JSON-encoded []string. A missing extension is
// not an error — nil is returned instead.
func readAdditionalApiServerProxyAddresses(kubeconfigPath string) ([]string, error) {
	cfg, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	ctx := cfg.Contexts[cfg.CurrentContext]
	if ctx == nil {
		return nil, nil
	}
	cluster := cfg.Clusters[ctx.Cluster]
	if cluster == nil {
		return nil, nil
	}

	ext, ok := cluster.Extensions["additionalApiServerProxyAddresses"]
	if !ok {
		return nil, nil
	}

	unknown, ok := ext.(*apimachineryruntime.Unknown)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T for additionalApiServerProxyAddresses extension", ext)
	}

	var addresses []string
	if err := json.Unmarshal(unknown.Raw, &addresses); err != nil {
		return nil, fmt.Errorf("failed to unmarshal additionalApiServerProxyAddresses: %w", err)
	}
	return addresses, nil
}

func createDirectories(workerCtx *typ.WorkerContext) error {
	dirs := []string{
		workerCtx.BinDir,
		workerCtx.KubeletStateDir,
		workerCtx.KubeletPKIPath,
		workerCtx.KubeletStaticPodPath,
		workerCtx.ContainerdState,
		workerCtx.ContainerdRoot,
		filepath.Dir(workerCtx.KubeletPKICaCertPath),
		filepath.Dir(workerCtx.KubeletConfigFile),
		filepath.Dir(workerCtx.KubeletLogFile),
		filepath.Dir(workerCtx.ContainerdConfig),
		filepath.Dir(workerCtx.ContainerdAddress),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	return nil
}
