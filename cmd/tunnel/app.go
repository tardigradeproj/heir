package tunnel

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/provision/worker/typ"
	"github.com/tardigradeproj/heir/pkg/tunnel/agent"
	"github.com/tardigradeproj/heir/pkg/tunnel/server"
)

type envBinding struct {
	flag string
	env  string
}

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
	root := &cobra.Command{
		Use:   "tunnel",
		Short: "Plane tunnel components",
	}
	root.AddCommand(agentCmd())
	root.AddCommand(serverCmd())
	return root
}

func agentCmd() *cobra.Command {
	var (
		certPath    string
		keyPath     string
		caCertPath  string
		serverAddr  string
		kubeletAddr string
		keepAlive   time.Duration
	)

	envBindings := []envBinding{
		{"cert", "TUNNEL_AGENT_CERT"},
		{"key", "TUNNEL_AGENT_KEY"},
		{"ca-cert", "TUNNEL_AGENT_CA_CERT"},
		{"server-addr", "TUNNEL_AGENT_SERVER_ADDR"},
		{"kubelet-addr", "TUNNEL_AGENT_KUBELET_ADDR"},
		{"keep-alive", "TUNNEL_AGENT_KEEP_ALIVE"},
	}

	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run the plane tunnel agent on a worker node",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			bindEnvs(cmd, envBindings)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, os.Interrupt)
			defer stop()

			a, err := agent.New(certPath, keyPath, caCertPath, serverAddr, kubeletAddr, keepAlive)
			if err != nil {
				return err
			}
			return a.Start(ctx)
		},
	}
	wrkCtx := typ.NewWorkerContextWithDefaults()
	cmd.Flags().StringVar(&certPath, "cert", fmt.Sprintf("%s/kubelet.crt", wrkCtx.KubeletPKIPath), "Client certificate path (env: TUNNEL_AGENT_CERT).")
	cmd.Flags().StringVar(&keyPath, "key", fmt.Sprintf("%s/kubelet.key", wrkCtx.KubeletPKIPath), "Client key path (env: TUNNEL_AGENT_KEY).")
	cmd.Flags().StringVar(&caCertPath, "ca-cert", wrkCtx.KubeletPKICaCertPath, "CA certificate path (env: TUNNEL_AGENT_CA_CERT).")
	cmd.Flags().StringVar(&serverAddr, "server-addr", "", "Tunnel server address host:port (env: TUNNEL_AGENT_SERVER_ADDR).")
	cmd.Flags().StringVar(&kubeletAddr, "kubelet-addr", "127.0.0.1:10250", "Kubelet address host:port (env: TUNNEL_AGENT_KUBELET_ADDR).")
	cmd.Flags().DurationVar(&keepAlive, "keep-alive", 12*time.Second, "Connection keep-alive interval (env: TUNNEL_AGENT_KEEP_ALIVE).")

	_ = cmd.MarkFlagRequired("server-addr")

	return cmd
}

func serverCmd() *cobra.Command {
	var (
		tunnelCert   string
		tunnelKey    string
		tunnelCACert string
		tunnelAddr   string
		egressCert   string
		egressKey    string
		egressCACert string
		egressAddr   string
		keepAlive    time.Duration
	)

	envBindings := []envBinding{
		{"tunnel-cert", "TUNNEL_SERVER_TUNNEL_CERT"},
		{"tunnel-key", "TUNNEL_SERVER_TUNNEL_KEY"},
		{"tunnel-ca-cert", "TUNNEL_SERVER_TUNNEL_CA_CERT"},
		{"tunnel-addr", "TUNNEL_SERVER_TUNNEL_ADDR"},
		{"egress-cert", "TUNNEL_SERVER_EGRESS_CERT"},
		{"egress-key", "TUNNEL_SERVER_EGRESS_KEY"},
		{"egress-ca-cert", "TUNNEL_SERVER_EGRESS_CA_CERT"},
		{"egress-addr", "TUNNEL_SERVER_EGRESS_ADDR"},
		{"keep-alive", "TUNNEL_SERVER_KEEP_ALIVE"},
	}

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the plane tunnel server on the management cluster",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			bindEnvs(cmd, envBindings)
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, os.Interrupt)
			defer stop()

			srv, err := server.New(
				&server.ListenerConfig{
					CertPath:   tunnelCert,
					KeyPath:    tunnelKey,
					CaCertPath: tunnelCACert,
					Addr:       tunnelAddr,
				},
				&server.ListenerConfig{
					CertPath:   egressCert,
					KeyPath:    egressKey,
					CaCertPath: egressCACert,
					Addr:       egressAddr,
				},
				keepAlive,
			)
			if err != nil {
				return err
			}
			return srv.Serve(ctx)
		},
	}

	cmd.Flags().StringVar(&tunnelCert, "tunnel-cert", "", "Tunnel listener certificate path (env: TUNNEL_SERVER_TUNNEL_CERT).")
	cmd.Flags().StringVar(&tunnelKey, "tunnel-key", "", "Tunnel listener key path (env: TUNNEL_SERVER_TUNNEL_KEY).")
	cmd.Flags().StringVar(&tunnelCACert, "tunnel-ca-cert", "", "Tunnel listener CA certificate path (env: TUNNEL_SERVER_TUNNEL_CA_CERT).")
	cmd.Flags().StringVar(&tunnelAddr, "tunnel-addr", ":8443", "Tunnel listener address host:port (env: TUNNEL_SERVER_TUNNEL_ADDR).")
	cmd.Flags().StringVar(&egressCert, "egress-cert", "", "Egress selector listener certificate path (env: TUNNEL_SERVER_EGRESS_CERT).")
	cmd.Flags().StringVar(&egressKey, "egress-key", "", "Egress selector listener key path (env: TUNNEL_SERVER_EGRESS_KEY).")
	cmd.Flags().StringVar(&egressCACert, "egress-ca-cert", "", "Egress selector listener CA certificate path (env: TUNNEL_SERVER_EGRESS_CA_CERT).")
	cmd.Flags().StringVar(&egressAddr, "egress-addr", ":8444", "Egress selector listener address host:port (env: TUNNEL_SERVER_EGRESS_ADDR).")
	cmd.Flags().DurationVar(&keepAlive, "keep-alive", 30*time.Second, "Connection keep-alive interval (env: TUNNEL_SERVER_KEEP_ALIVE).")

	_ = cmd.MarkFlagRequired("tunnel-cert")
	_ = cmd.MarkFlagRequired("tunnel-key")
	_ = cmd.MarkFlagRequired("tunnel-ca-cert")
	_ = cmd.MarkFlagRequired("egress-cert")
	_ = cmd.MarkFlagRequired("egress-key")
	_ = cmd.MarkFlagRequired("egress-ca-cert")

	return cmd
}
