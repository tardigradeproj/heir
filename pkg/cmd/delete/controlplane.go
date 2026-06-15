package delete

import (
	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/provision/controlplane"
)

type controlPlaneFlagpole struct {
	Kubeconfig string
	Namespace  string
}

func controlPlaneCommand() *cobra.Command {
	flags := &controlPlaneFlagpole{}
	cmd := &cobra.Command{
		Use:   "controlplane <cluster-name>",
		Short: "Delete a control plane cluster from the management cluster",
		Long:  "Delete a control plane cluster from the management cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return controlplane.Delete(cmd.Context(),
				controlplane.WithName(args[0]),
				controlplane.WithKubeconfig(flags.Kubeconfig),
				controlplane.WithNamespace(flags.Namespace),
			)
		},
	}
	cmd.Flags().StringVar(
		&flags.Kubeconfig,
		"kubeconfig",
		"",
		"sets kubeconfig path of the host cluster instead of $KUBECONFIG or $HOME/.kube/config",
	)
	cmd.Flags().StringVar(
		&flags.Namespace,
		"namespace",
		"default",
		"namespace where the cluster is provisioned",
	)
	return cmd
}
