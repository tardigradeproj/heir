package delete

import (
	"github.com/spf13/cobra"
	"sigs.k8s.io/kind/pkg/errors"
)

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete one of [controlplane]",
		Long:  "Delete one of [controlplane]",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return errors.New("subcommand is required")
		},
	}
	cmd.AddCommand(controlPlaneCommand())
	return cmd
}
