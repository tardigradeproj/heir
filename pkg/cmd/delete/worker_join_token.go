package delete

import (
	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/provision/token"
)

type workerJoinTokenDeleteFlagpole struct {
	Kubeconfig string
}

func workerJoinTokenDeleteCommand() *cobra.Command {
	flags := &workerJoinTokenDeleteFlagpole{}
	cmd := &cobra.Command{
		Use:     "workerJoinToken <name>",
		Aliases: []string{"token"},
		Short:   "Delete a worker join token from a tenant cluster",
		Long:    "Delete a worker join token from a tenant cluster",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return token.Delete(cmd.Context(),
				token.WithName(args[0]),
				token.WithKubeconfig(flags.Kubeconfig),
			)
		},
	}
	cmd.Flags().StringVar(
		&flags.Kubeconfig,
		"kubeconfig",
		"",
		"sets kubeconfig path of the tenant cluster (not the management cluster) instead of "+
			"$KUBECONFIG or $HOME/.kube/config",
	)
	return cmd
}
