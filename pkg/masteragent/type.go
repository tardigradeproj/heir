package masteragent

import (
	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/k3s-io/kine/pkg/metrics"
)

type Config struct {
	Storage        endpoint.Config
	StorageMetrics metrics.Config
}
