package masteragent

import (
	"context"
	"runtime"
	"strings"
	"sync"

	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/k3s-io/kine/pkg/metrics"
	log "github.com/sirupsen/logrus"
)

func Run(ctx context.Context, conf Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	log.AddHook(&kineLogHook{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- SyncKubernetesEndpoints(ctx)
	}()
	go func() {
		errCh <- RunCSRAutoApprover(ctx)
	}()
	go func() {
		errCh <- runKine(ctx, conf.Storage, conf.StorageMetrics)
	}()
	go func() {
		errCh <- RunReadyz(ctx, conf.Healthz)
	}()
	return <-errCh
}

type kineLogHook struct{}

func (h *kineLogHook) Levels() []log.Level { return log.AllLevels }

func (h *kineLogHook) Fire(entry *log.Entry) error {
	pcs := make([]uintptr, 15)
	n := runtime.Callers(4, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "k3s-io/kine") {
			entry.Data["component"] = "kine"
			return nil
		}
		if !more {
			break
		}
	}
	return nil
}

func runKine(ctx context.Context, config endpoint.Config, metricsCfg metrics.Config) (rerr error) {
	log.Info("starting storage")
	config.WaitGroup = &sync.WaitGroup{}
	_, err := endpoint.Listen(ctx, config)
	if err != nil {
		return err
	}
	go metrics.Serve(ctx, metricsCfg)
	defer func() {
		config.WaitGroup.Wait()
		if rerr == nil {
			rerr = ctx.Err()
		}
	}()

	return nil
}
