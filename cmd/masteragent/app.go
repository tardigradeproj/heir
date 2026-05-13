package masteragent

import (
	"fmt"
	"os"
	"time"

	"github.com/k3s-io/kine/pkg/drivers/generic"
	"github.com/k3s-io/kine/pkg/endpoint"
	"github.com/k3s-io/kine/pkg/metrics"
	"github.com/k3s-io/kine/pkg/tls"
	"github.com/spf13/cobra"
	"github.com/tardigrade-runtime/samaritano/pkg/masteragent"
)

// envBinding maps a flag name to its environment variable override.
type envBinding struct {
	flag string
	env  string
}

// bindEnvs wires environment variable overrides to flags that were not
// explicitly set on the command line. Call this inside PersistentPreRunE so
// that explicit CLI values always take precedence over env vars.
func bindEnvs(cmd *cobra.Command, bindings []envBinding) {
	for _, b := range bindings {
		if cmd.Flags().Changed(b.flag) {
			continue
		}
		if val := os.Getenv(b.env); val != "" {
			_ = cmd.Flags().Set(b.flag, val)
		}
	}
}

func Cmd() *cobra.Command {
	cfg := masteragent.Config{
		Storage: endpoint.Config{
			Listener: "0.0.0.0:2379",
			BackendTLSConfig: tls.Config{
				SkipVerify: false,
			},
			LogFormat: "plain",
			ConnectionPoolConfig: generic.ConnectionPoolConfig{
				MaxOpen:     10,
				MaxIdle:     0,
				MaxLifetime: 0,
			},
			NotifyInterval:        time.Second * 5,
			CompactInterval:       2 * time.Minute,
			CompactIntervalJitter: 0,
			CompactTimeout:        5 * time.Second,
			CompactMinRetain:      1000,
			CompactBatchSize:      1000,
			PollBatchSize:         500,
		},
		StorageMetrics: metrics.Config{
			ServerAddress: "0.0.0.0:8081",
		},
	}

	envBindings := []envBinding{
		{"storage-endpoint", "SAMARITANO_STORAGE_ENDPOINT"},
		{"storage-emulated-etcd-version", "SAMARITANO_STORAGE_EMULATED_ETCD_VERSION"},
		{"storage-metrics-bind-address", "SAMARITANO_STORAGE_METRICS_BIND_ADDRESS"},
	}

	cmd := &cobra.Command{
		Use:   "master-agent",
		Short: "Run the Samaritano master agent",
		Long:  "Starts and supervises the control plane components: storage layer (kine), kube-apiserver, kube-controller-manager, and kube-scheduler. Once the API server is healthy, applies cluster bootstrap manifests (kube-proxy, CoreDNS, CNI) and keeps all components running with automatic restarts.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			bindEnvs(cmd, envBindings)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("starting master-agent", cfg.Storage.Endpoint)
			return masteragent.Run(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(
		&cfg.Storage.Endpoint,
		"storage-endpoint",
		"",
		"Storage backend endpoint; defaults to SQLite (env: SAMARITANO_STORAGE_ENDPOINT)",
	)
	cmd.Flags().StringVar(
		&cfg.Storage.EmulatedETCDVersion,
		"storage-emulated-etcd-version",
		"3.5.13",
		"The emulated etcd version to return on a call to the status endpoint. Defaults to 3.5.13, in order to indicate support for watch progress notifications.",
	)
	cmd.Flags().StringVar(
		&cfg.StorageMetrics.ServerAddress,
		"storage-metrics-bind-address",
		":8081",
		"The address the metric endpoint binds to. Default :8081, set 0 to disable metrics serving.",
	)
	return cmd
}
