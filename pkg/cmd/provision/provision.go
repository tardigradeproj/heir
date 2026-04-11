package provision

import (
	"github.com/spf13/cobra"
	"github.com/tardigrade-runtime/samaritano/pkg/cmd/token/generate"
	"sigs.k8s.io/kind/pkg/errors"
)

// NewCommand returns a new cobra.Command for token management
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "Provision one of [worker, controlplane]",
		Long:  "Manage one of [worker, controlplane]",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := cmd.Help()
			if err != nil {
				return err
			}
			return errors.New("subcommand is required")
		},
	}
	// add subcommands
	cmd.AddCommand(generate.NewCommand())
	cmd.AddCommand(workerProvisionCommand())
	return cmd
}
