package provision

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/provision/controlplane"
)

type controlplaneProvisionFlagpole struct {
	Name       string
	Namespace  string
	Kubeconfig string
	Set        []string
	Wait       bool
	Timeout    time.Duration
}

func controlplaneProvisionCommand() *cobra.Command {
	flags := &controlplaneProvisionFlagpole{}
	cmd := &cobra.Command{
		Use:   "controlplane",
		Short: "Provision a control plane on the management cluster",
		Long:  "Provision a control plane on the management cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			return controlplane.Provision(cmd.Context(),
				controlplane.WithName(flags.Name),
				controlplane.WithNamespace(flags.Namespace),
				controlplane.WithKubeconfig(flags.Kubeconfig),
				controlplane.WithSet(flags.Set),
				controlplane.WithWait(flags.Wait),
				controlplane.WithTimeout(flags.Timeout),
			)
		},
	}
	cmd.Flags().StringVar(
		&flags.Name,
		"name",
		"",
		"name of the Runtime to create",
	)
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
	cobra.CheckErr(cmd.MarkFlagRequired("name"))
	return cmd
}
