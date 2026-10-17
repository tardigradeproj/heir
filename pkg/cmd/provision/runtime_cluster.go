package provision

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/provision/controlplane"
)

type runtimeClusterProvisionFlagpole struct {
	Namespace  string
	Kubeconfig string
	Set        []string
	Wait       bool
	Timeout    time.Duration
}

func runtimeClusterProvisionCommand() *cobra.Command {
	flags := &runtimeClusterProvisionFlagpole{}
	cmd := &cobra.Command{
		Use:     "runtimeCluster <name>",
		Aliases: []string{"cluster"},
		Short:   "Provision a control plane on the management cluster",
		Long:    "Provision a control plane on the management cluster",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return controlplane.Provision(cmd.Context(),
				controlplane.WithName(args[0]),
				controlplane.WithNamespace(flags.Namespace),
				controlplane.WithKubeconfig(flags.Kubeconfig),
				controlplane.WithSet(flags.Set),
				controlplane.WithWait(flags.Wait),
				controlplane.WithTimeout(flags.Timeout),
			)
		},
	}
	cmd.Flags().StringVar(
		&flags.Namespace,
		"namespace",
		"default",
		"namespace where the control plane will be provisioned",
	)
	cmd.Flags().StringVar(
		&flags.Kubeconfig,
		"kubeconfig",
		"",
		"sets kubeconfig path of the management cluster instead of $KUBECONFIG or $HOME/.kube/config",
	)
	cmd.Flags().StringArrayVar(
		&flags.Set,
		"set",
		nil,
		"set a Runtime spec value on the command line, relative to spec (can specify multiple "+
			"or separate values with commas: cluster.apiServer.sans[0]=foo,controlPlane.planeTunnel.server.image=abc)",
	)
	cmd.Flags().BoolVar(
		&flags.Wait,
		"wait",
		true,
		"block until the Runtime's Available condition is True",
	)
	cmd.Flags().DurationVar(
		&flags.Timeout,
		"timeout",
		5*time.Minute,
		"how long --wait blocks before giving up",
	)
	return cmd
}
