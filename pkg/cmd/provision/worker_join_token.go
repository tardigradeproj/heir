package provision

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/provision/token"
)

type workerJoinTokenProvisionFlagpole struct {
	TTL        time.Duration
	Kubeconfig string
	Wait       bool
	Timeout    time.Duration
}

func workerJoinTokenProvisionCommand() *cobra.Command {
	flags := &workerJoinTokenProvisionFlagpole{}
	cmd := &cobra.Command{
		Use:     "workerJoinToken <name>",
		Aliases: []string{"token"},
		Short:   "Generate a worker join token on a tenant cluster",
		Long:    "Generate a worker join token on a tenant cluster",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return token.Provision(cmd.Context(),
				token.WithName(args[0]),
				token.WithTTL(flags.TTL),
				token.WithKubeconfig(flags.Kubeconfig),
				token.WithWait(flags.Wait),
				token.WithTimeout(flags.Timeout),
			)
		},
	}
	cmd.Flags().DurationVar(
		&flags.TTL,
		"ttl",
		3*time.Hour,
		"how long the minted token remains valid, starting from when it's issued",
	)
	cmd.Flags().StringVar(
		&flags.Kubeconfig,
		"kubeconfig",
		"",
		"sets kubeconfig path of the tenant cluster (not the management cluster) instead of "+
			"$KUBECONFIG or $HOME/.kube/config",
	)
	cmd.Flags().BoolVar(
		&flags.Wait,
		"wait",
		true,
		"block until the WorkerJoinToken's Ready condition is True",
	)
	cmd.Flags().DurationVar(
		&flags.Timeout,
		"timeout",
		time.Minute,
		"how long --wait blocks before giving up",
	)
	return cmd
}
