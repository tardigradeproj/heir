package masteragent

import (
	"time"

	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/k3s-io/kine/pkg/metrics"
)

type Config struct {
	Storage        endpoint.Config
	StorageMetrics metrics.Config
	Healthz        HealthzConfig
	PlaneTunnel    PlaneTunnelConfig
}

type PlaneTunnelConfig struct {
	ProxyHostname         string
	SynchronizationPeriod time.Duration
	HostsPath             string
}
