package provision

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker"
	"github.com/tardigrade-runtime/samaritano/pkg/provision/worker/typ"
)

type workerFlagpole struct {
	Token            string
	KubeletExtraArgs map[string]string
}

func workerProvisionCommand() *cobra.Command {
	flags := &workerFlagpole{}
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Set up worker node bootstrap prerequisites",
		Long:  "Prepares all required prerequisites for worker node bootstrap and starts the long-running Samaritano process responsible for managing and maintaining worker node dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := worker.Join(cmd.Context(), flags.Token,
				typ.WithKubeletExtraArgs(flags.KubeletExtraArgs),
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
		&flags.KubeletExtraArgs,
		"kubelet-extra-args",
		map[string]string{},
		"extra arguments to pass to kubelet, as a list of key=value pairs",
	)

	return cmd
}

func WorkerRunCommand() *cobra.Command {
	flags := &workerFlagpole{}
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Initialize and join a worker node to the cluster",
		Long:  "Initializes a worker node and joins it to the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := worker.Run(cmd.Context(), typ.WithKubeletExtraArgs(flags.KubeletExtraArgs))
			if err != nil {
				return fmt.Errorf("failed to run worker node: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringToStringVar(
		&flags.KubeletExtraArgs,
		"kubelet-extra-args",
		map[string]string{},
		"extra arguments to pass to kubelet, as a list of key=value pairs",
	)

	return cmd
}
