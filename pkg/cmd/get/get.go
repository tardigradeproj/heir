package get

import (
	"github.com/spf13/cobra"
	"sigs.k8s.io/kind/pkg/errors"
)

// NewCommand returns a new cobra.Command for reading cluster resources.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get one of [workerJoinToken, runtimeCluster]",
		Long:  "Get one of [workerJoinToken, runtimeCluster]",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return errors.New("subcommand is required")
		},
	}
	cmd.AddCommand(workerJoinTokenGetCommand())
	cmd.AddCommand(runtimeClusterGetCommand())
	return cmd
}
