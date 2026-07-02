package masteragent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/tardigradeproj/heir/pkg/network/hosts"
)

type ReportResponse struct {
	Node []ConnectedNode `json:"node"`
}

type ConnectedNode struct {
	Name string `json:"name"` // node name, e.g. "worker2"
}

type PlaneTunnel struct {
	ProxyHostname         string        `json:"proxyHostname"`
	SynchronizationPeriod time.Duration `json:"synchronizationPeriod"`

	hostsManager *hosts.Manager
	resolvePods  func(ctx context.Context, hostname string) ([]string, error)
	fetchReport  func(ctx context.Context, podIP string) (*ReportResponse, error)
}

func NewPlaneTunnel(cfg PlaneTunnelConfig) *PlaneTunnel {
	return &PlaneTunnel{
		ProxyHostname:         cfg.ProxyHostname,
		SynchronizationPeriod: cfg.SynchronizationPeriod,
		hostsManager:          hosts.New(cfg.HostsPath),
		resolvePods:           resolvePlaneTunnelPods,
		fetchReport:           fetchReport,
	}
}

func (t *PlaneTunnel) Run(ctx context.Context) error {
	// Flush any managed state left over from a previous run before rebuilding.
	if err := t.hostsManager.Sync(map[string]string{}); err != nil {
		return fmt.Errorf("flushing hosts on startup: %w", err)
	}

	for {
		log.Debug("reconciling plane tunnel connected nodes")
		if err := t.reconcile(); err != nil {
			log.WithError(err).Warn("plane tunnel reconcile failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.SynchronizationPeriod):
		}
	}
}

func (t *PlaneTunnel) reconcile() error {
	ctx := context.Background()

	podIPs, err := t.resolvePods(ctx, t.ProxyHostname)
	if err != nil {
		return fmt.Errorf("resolving plane tunnel pods: %w", err)
	}

	// nodeRoutes maps worker hostname → plane tunnel pod IP.
	nodeRoutes := make(map[string]string)

	for _, podIP := range podIPs {
		report, err := t.fetchReport(ctx, podIP)
		if err != nil {
			log.WithField("pod", podIP).WithError(err).Warn("skipping pod: failed to fetch report")
			continue
		}
		for _, node := range report.Node {
			nodeRoutes[node.Name] = podIP
		}
	}

	if err := t.hostsManager.Sync(nodeRoutes); err != nil {
		return fmt.Errorf("syncing hosts: %w", err)
	}

	log.WithField("nodes", len(nodeRoutes)).Debug("plane tunnel reconcile complete")
	return nil
}

// resolvePlaneTunnelPods resolves a headless Service DNS name to the Pod IPs
// of all ready plane tunnel instances.
func resolvePlaneTunnelPods(ctx context.Context, hostname string) ([]string, error) {
	addrs, err := net.DefaultResolver.LookupHost(ctx, hostname)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", hostname, err)
	}
	return addrs, nil
}

// fetchReport calls a plane tunnel instance and returns the nodes currently connected to it.
func fetchReport(ctx context.Context, podIP string) (*ReportResponse, error) {
	url := "http://" + podIP + "/v1/report"
	lg := log.WithField("pod", podIP)
	lg.Debug("fetching plane tunnel report")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", podIP, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		lg.WithError(err).Warn("plane tunnel report request failed")
		return nil, fmt.Errorf("calling %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		lg.WithField("status", resp.Status).Error("plane tunnel report returned unexpected status")
		return nil, fmt.Errorf("unexpected status from %s: %s", url, resp.Status)
	}

	var report ReportResponse
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		lg.WithError(err).Warn("failed to decode plane tunnel report")
		return nil, fmt.Errorf("decoding response from %s: %w", url, err)
	}

	lg.WithField("nodes", len(report.Node)).Debug("received plane tunnel report")
	return &report, nil
}
