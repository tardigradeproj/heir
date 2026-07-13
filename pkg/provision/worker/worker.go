package worker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	btsp "github.com/tardigradeproj/heir/pkg/provision/worker/bootstrap"
	"github.com/tardigradeproj/heir/pkg/provision/worker/component"
	"github.com/tardigradeproj/heir/pkg/provision/worker/proxy"
	"github.com/tardigradeproj/heir/pkg/provision/worker/sys"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	apiServerStream = "apiserver"
)

func Run(ctx context.Context, opts ...typ.Option) error {
	var proxyErr chan error
	workerCtx := typ.NewWorkerContextWithDefaults()
	for _, opt := range opts {
		opt(workerCtx)
	}

	log := logrus.WithField("hostname", hostname)
	log.Info("starting worker node provisioning")

	if err := createDirectories(workerCtx); err != nil {
		return fmt.Errorf("failed to create config directories: %w", err)
	}
	if err := sys.Configure(); err != nil {
		return fmt.Errorf("failed to configure host system: %w", err)
	}

	// Phase 1 — start the API server proxy before TLS bootstrap so the kubelet
	// bootstrap client can reach the API server through the local proxy.
	proxyManager := proxy.NewManager(ctx)
	initialEndpoints, err := resolveInitialAPIServerEndpoints(log, workerCtx)
	if err != nil {
		return fmt.Errorf("failed to resolve initial API server endpoints: %w", err)
	}
	apiUpstream := proxyManager.AddUpstream(apiServerStream, initialEndpoints)
	proxyManager.AddDownstream(apiServerStream, workerCtx.ApiServerWorkerProxyServerAddress)
	go func() {
		log.WithField("addr", workerCtx.ApiServerWorkerProxyServerAddress).Info("starting local proxy manager")
		if err := proxyManager.Link(apiServerStream, apiServerStream); err != nil {
			proxyErr <- err
			log.WithError(err).Error("proxy manager exited with error")
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

	// Phase 2 — update the API server upstream with definitive addresses from the profile.
	apiUpstream.Update(endpointsFromAddresses(
		profile.ControlPlaneEndpoint.Addresses,
		int(profile.ControlPlaneEndpoint.APIServer.Port),
	))

	runners := []Runner{
		component.NewContainerd(workerCtx),
		component.NewKubelet(workerCtx, profile, hostname),
	}
	if slices.Contains([]string{"flannel"}, profile.CNIProvider) {
		runners = append(runners, component.NewCni(workerCtx))
	}

	log.Debug("setting up components")
	for _, rn := range runners {
		if err := rn.Setup(); err != nil {
			return fmt.Errorf("failed to setup %T: %w", rn, err)
		}
	}

	log.Info("starting components")
	errCh := make(chan error, len(runners))
	for _, rn := range runners {
		go func(r Runner) {
			if err := r.Run(ctx); err != nil {
				errCh <- fmt.Errorf("%T: %w", r, err)
			}
		}(rn)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var runErr error
	select {
	case proxyErr := <-proxyErr:
		log.WithError(proxyErr).Error("proxy manager exited with error")
	case sig := <-sigCh:
		log.WithField("signal", sig).Info("received shutdown signal")
	case runErr = <-errCh:
		log.WithError(runErr).Error("component exited with error")
	case <-ctx.Done():
		log.Debug("context cancelled")
	}

	log.Info("tearing down components")
	teardownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, rn := range runners {
		if err := rn.Teardown(teardownCtx); err != nil {
			log.WithError(err).WithField("component", fmt.Sprintf("%T", rn)).Error("teardown failed")
		}
	}

	log.Info("worker node shut down")
	return runErr
}

// resolveInitialAPIServerEndpoints returns the endpoints used to seed the API
// server proxy before TLS bootstrap. It prefers the cached node profile on disk;
// if absent it falls back to parsing the bootstrap kubeconfig server URL.
func resolveInitialAPIServerEndpoints(log *logrus.Entry, workerCtx *typ.WorkerContext) ([]proxy.Endpoint, error) {
	_, statErr := os.Stat(workerCtx.NodeProfileLocalFilePath)
	if statErr == nil {
		nodeProfile := &typ.NodeProfile{}
		if err := nodeProfile.Load(workerCtx.NodeProfileLocalFilePath); err != nil {
			return nil, err
		}
		log.WithField("source", "node-profile").Debug("seeding API server proxy from cached profile")
		return endpointsFromAddresses(
			nodeProfile.ControlPlaneEndpoint.Addresses,
			int(nodeProfile.ControlPlaneEndpoint.APIServer.Port),
		), nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("failed to stat node profile: %w", statErr)
	}

	log.WithField("source", "bootstrap-kubeconfig").Warn("node profile not found, seeding API server proxy from bootstrap kubeconfig")
	return parseEndpointsFromBootstrapKubeconfig(workerCtx.KubeletBootstrapKubeconfigPath)
}

// parseEndpointsFromBootstrapKubeconfig extracts unique API server endpoints
// by parsing the server URL from each cluster entry of the kubeconfig.
func parseEndpointsFromBootstrapKubeconfig(kubeconfigPath string) ([]proxy.Endpoint, error) {
	cfg, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load bootstrap kubeconfig: %w", err)
	}

	seen := map[string]bool{}
	var endpoints []proxy.Endpoint
	for _, cluster := range cfg.Clusters {
		if cluster.Server == "" {
			continue
		}
		u, err := url.Parse(cluster.Server)
		if err != nil || u.Hostname() == "" {
			continue
		}
		host := u.Hostname()
		if seen[host] {
			continue
		}
		seen[host] = true
		port := 443
		if p := u.Port(); p != "" {
			if n, err := strconv.Atoi(p); err == nil {
				port = n
			}
		}
		endpoints = append(endpoints, proxy.Endpoint{Host: host, Port: port})
	}
	return endpoints, nil
}

// endpointsFromAddresses converts a list of hosts sharing a common port into
// a slice of proxy.Endpoint values.
func endpointsFromAddresses(hosts []string, port int) []proxy.Endpoint {
	eps := make([]proxy.Endpoint, len(hosts))
	for i, h := range hosts {
		eps[i] = proxy.Endpoint{Host: h, Port: port}
	}
	return eps
}

func createDirectories(workerCtx *typ.WorkerContext) error {
	dirs := []string{
		workerCtx.BinDir,
		workerCtx.KubeletStateDir,
		workerCtx.KubeletPKIPath,
		workerCtx.KubeletStaticPodPath,
		workerCtx.ContainerdState,
		workerCtx.ContainerdRoot,
		workerCtx.CNIBinFolderPath,
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
