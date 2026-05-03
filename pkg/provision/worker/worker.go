package worker

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	btsp "github.com/tardigrade-runtime/samaritano/pkg/provision/worker/bootstrap"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/component"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/sys"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
)

func Worker(ctx context.Context, opts ...typ.Option) error {
	workerCtx := typ.NewWorkerContextWithDefaults()
	for _, opt := range opts {
		opt(workerCtx)
	}
	log := logrus.WithField("hostname", hostname)
	log.Info("starting worker node provisioning")

	log.Debug("configuring host system")
	if err := sys.Configure(); err != nil {
		return fmt.Errorf("failed to setup host: %w", err)
	}

	log.Debug("performing TLS bootstrap")
	if err := btsp.BootstrapKubeletClientConfig(ctx, workerCtx, hostname); err != nil {
		return fmt.Errorf("failed to perform TLS bootstrap: %w", err)
	}

	log.Debug("reading worker node profile")
	profile, err := btsp.ReadWorkerNodeProfile(ctx, workerCtx)
	if err != nil {
		return fmt.Errorf("failed to read worker node profile: %w", err)
	}

	runners := []Runner{
		component.NewContainerd(workerCtx),
		component.NewKubelet(workerCtx, profile.KubeletConfiguration, hostname),
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
