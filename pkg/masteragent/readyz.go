package masteragent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type HealthzConfig struct {
	Port                  int
	APIServerPort         int
	ControllerManagerPort int
	SchedulerPort         int
	PeriodSeconds         int
}

type componentState struct {
	name       string
	port       int
	healthPath string
	healthy    bool
}

func RunReadyz(ctx context.Context, cfg HealthzConfig) error {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}

	components := []*componentState{
		{name: "kube-apiserver", port: cfg.APIServerPort, healthPath: "/readyz"},
		{name: "kube-controller-manager", port: cfg.ControllerManagerPort, healthPath: "/healthz"},
		{name: "kube-scheduler", port: cfg.SchedulerPort, healthPath: "/readyz"},
	}

	var mu sync.RWMutex

	go func() {
		probeComponents(client, components, &mu)
		ticker := time.NewTicker(time.Duration(cfg.PeriodSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probeComponents(client, components, &mu)
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", aggregateHandler(components, &mu))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		server.Close()
	}()

	log.WithField("port", cfg.Port).Info("starting readyz server")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func probeComponents(client *http.Client, components []*componentState, mu *sync.RWMutex) {
	for _, c := range components {
		healthy := probeURL(client, fmt.Sprintf("https://localhost:%d%s", c.port, c.healthPath))

		mu.Lock()
		prevHealthy := c.healthy
		c.healthy = healthy
		mu.Unlock()

		lg := log.WithField("component", c.name)
		if !healthy && prevHealthy {
			lg.Info("became unhealthy")
		} else if healthy && !prevHealthy {
			lg.Info("is now healthy")
		} else if !healthy {
			lg.Info("is unhealthy")
		}
	}
}

func probeURL(client *http.Client, url string) bool {
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func aggregateHandler(components []*componentState, mu *sync.RWMutex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()

		var notOK []string
		for _, c := range components {
			if !c.healthy {
				notOK = append(notOK, c.name)
			}
		}

		if len(notOK) == 0 {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "ok")
			return
		}

		w.WriteHeader(http.StatusServiceUnavailable)
		for _, name := range notOK {
			fmt.Fprintf(w, "[+]%s not ok\n", name)
		}
	}
}
