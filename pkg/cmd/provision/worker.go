package provision

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker"
)

type workerFlagpole struct {
	Token              string
	NodeLabels         map[string]string
	KubeletExtraArgs   map[string]string
	KubeProxyExtraArgs map[string]string
}

func workerProvisionCommand() *cobra.Command {
	flags := &workerFlagpole{}
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Provision a worker node and join it to the cluster",
		Long:  "Provision a worker node and join it to the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := worker.Join(cmd.Context(), flags.Token,
				worker.WithKubeProxyExtraArgs(flags.KubeProxyExtraArgs),
				worker.WithNodeLabels(flags.NodeLabels),
				worker.WithKubeletExtraArgs(flags.KubeletExtraArgs),
			)
			if err != nil {
				return fmt.Errorf("failed to join worker node: %w", err)
			}
			fmt.Println("worker node successfully setup, your cluster is growing...")
			return nil
		},
	}
	cmd.Flags().StringVar(
		&flags.Token,
		"token",
		"",
		"bootstrap token used to join the cluster",
	)
	cmd.Flags().StringToStringVar(
		&flags.NodeLabels,
		"node-label",
		map[string]string{},
		"labels to register the node with, as a list of key=value pairs",
	)
	cmd.Flags().StringToStringVar(
		&flags.KubeletExtraArgs,
		"kubelet-extra-args",
		map[string]string{},
		"extra arguments to pass to kubelet, as a list of key=value pairs",
	)
	cmd.Flags().StringToStringVar(
		&flags.KubeProxyExtraArgs,
		"kube-proxy-extra-args",
		map[string]string{},
		"extra arguments to pass to kube-proxy, as a list of key=value pairs",
	)
	return cmd
}
