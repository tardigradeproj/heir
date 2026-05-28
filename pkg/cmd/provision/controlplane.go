package provision

import (
	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/provision/controlplane"
)

type controlplaneFlagpole struct {
	Name              string
	Config            string
	Kubeconfig        string
	ClusterKubeconfig string
	Namespace         string
}

func controlplaneProvisionCommand() *cobra.Command {
	flags := &controlplaneFlagpole{}
	cmd := &cobra.Command{
		Use:   "controlplane",
		Short: "Provision a control plane cluster on the host cluster",
		Long:  "Provision a control plane cluster on the host cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := controlplane.Provision(cmd.Context(),
				controlplane.WithConfig(flags.Config),
				controlplane.WithKubeconfig(flags.Kubeconfig),
				controlplane.WithClusterKubeconfig(flags.ClusterKubeconfig),
				controlplane.WithNamespace(flags.Namespace),
				controlplane.WithName(flags.Name),
			); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(
		&flags.Name,
		"name",
		"",
		"name of the cluster",
	)
	cmd.Flags().StringVar(
		&flags.Config,
		"config",
		"",
		"path to a heir config file",
	)
	cmd.Flags().StringVar(
		&flags.Kubeconfig,
		"kubeconfig",
		"",
		"sets kubeconfig path of the host cluster instead of $KUBECONFIG or $HOME/.kube/config",
	)
	cmd.Flags().StringVar(
		&flags.ClusterKubeconfig,
		"cluster-kubeconfig",
		"",
		"path to kubeconfig that will hold configuration of the newly created cluster, instead of $KUBECONFIG or $HOME/.kube/config",
	)
	cmd.Flags().StringVar(
		&flags.Namespace,
		"namespace",
		"",
		"namespace where the cluster will be provisioned",
	)
	return cmd
}
