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
	"github.com/tardigradeproj/heir/pkg/masteragent"
)

const etcdVersion = "3.6.6"

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
		PlaneTunnel: masteragent.PlaneTunnelConfig{
			SynchronizationPeriod: 5 * time.Second,
			HostsPath:             "/etc/hosts",
		},
		Healthz: masteragent.HealthzConfig{
			Port:                  8084,
			APIServerPort:         6443,
			ControllerManagerPort: 10257,
			SchedulerPort:         10259,
			PeriodSeconds:         15,
		},
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
		{"plane-tunnel-proxy-hostname", "HEIR_PLANE_TUNNEL_PROXY_HOSTNAME"},
		{"plane-tunnel-sync-period", "HEIR_PLANE_TUNNEL_SYNC_PERIOD"},
		{"plane-tunnel-hosts-path", "HEIR_PLANE_TUNNEL_HOSTS_PATH"},
		{"storage-endpoint", "HEIR_STORAGE_ENDPOINT"},
		{"storage-emulated-etcd-version", "HEIR_STORAGE_EMULATED_ETCD_VERSION"},
		{"storage-metrics-bind-address", "HEIR_STORAGE_METRICS_BIND_ADDRESS"},
		{"healthz-port", "HEIR_HEALTHZ_PORT"},
		{"healthz-apiserver-port", "HEIR_HEALTHZ_APISERVER_PORT"},
		{"healthz-controller-manager-port", "HEIR_HEALTHZ_CONTROLLER_MANAGER_PORT"},
		{"healthz-scheduler-port", "HEIR_HEALTHZ_SCHEDULER_PORT"},
		{"healthz-period-seconds", "HEIR_HEALTHZ_PERIOD_SECONDS"},
	}

	cmd := &cobra.Command{
		Use:   "master-agent",
		Short: "Run the Heir master agent",
		Long:  "Starts and supervises the control plane components: storage layer (kine), kube-apiserver, kube-controller-manager, and kube-scheduler. Once the API server is healthy, applies cluster bootstrap manifests (kube-proxy, CoreDNS, CNI) and keeps all components running with automatic restarts.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			bindEnvs(cmd, envBindings)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return masteragent.Run(cmd.Context(), cfg)
		},
	}

	cmd.Flags().StringVar(
		&cfg.PlaneTunnel.ProxyHostname,
		"plane-tunnel-proxy-hostname",
		"",
		"Headless Service DNS name to resolve for plane tunnel pod IPs (env: HEIR_PLANE_TUNNEL_PROXY_HOSTNAME).",
	)
	cmd.Flags().DurationVar(
		&cfg.PlaneTunnel.SynchronizationPeriod,
		"plane-tunnel-sync-period",
		5*time.Second,
		"How often to reconcile hosts file against plane tunnel reports (env: HEIR_PLANE_TUNNEL_SYNC_PERIOD).",
	)
	cmd.Flags().StringVar(
		&cfg.PlaneTunnel.HostsPath,
		"plane-tunnel-hosts-path",
		"/etc/hosts",
		"Path to the hosts file to (env: HEIR_PLANE_TUNNEL_HOSTS_PATH).",
	)
	cmd.Flags().StringVar(
		&cfg.Storage.Endpoint,
		"storage-endpoint",
		"",
		"Storage backend endpoint; defaults to SQLite (env: HEIR_STORAGE_ENDPOINT)",
	)
	cmd.Flags().StringVar(
		&cfg.Storage.EmulatedETCDVersion,
		"storage-emulated-etcd-version",
		etcdVersion,
		fmt.Sprintf("The emulated etcd version to return on a call to the status endpoint. Defaults to %s, in order to indicate support for watch progress notifications.", etcdVersion),
	)
	cmd.Flags().StringVar(
		&cfg.StorageMetrics.ServerAddress,
		"storage-metrics-bind-address",
		":8081",
		"The address the metric endpoint binds to. Default :8081, set 0 to disable metrics serving.",
	)
	cmd.Flags().IntVar(
		&cfg.Healthz.Port,
		"healthz-port",
		8084,
		"Port for the readyz HTTP server (env: HEIR_HEALTHZ_PORT).",
	)
	cmd.Flags().IntVar(
		&cfg.Healthz.APIServerPort,
		"healthz-apiserver-port",
		6443,
		"Port to probe for kube-apiserver health (env: HEIR_HEALTHZ_APISERVER_PORT).",
	)
	cmd.Flags().IntVar(
		&cfg.Healthz.ControllerManagerPort,
		"healthz-controller-manager-port",
		10257,
		"Port to probe for kube-controller-manager health (env: HEIR_HEALTHZ_CONTROLLER_MANAGER_PORT).",
	)
	cmd.Flags().IntVar(
		&cfg.Healthz.SchedulerPort,
		"healthz-scheduler-port",
		10259,
		"Port to probe for kube-scheduler health (env: HEIR_HEALTHZ_SCHEDULER_PORT).",
	)
	cmd.Flags().IntVar(
		&cfg.Healthz.PeriodSeconds,
		"healthz-period-seconds",
		15,
		"How often (in seconds) to probe each component (env: HEIR_HEALTHZ_PERIOD_SECONDS).",
	)
	return cmd
}
