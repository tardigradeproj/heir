package token

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/cmd/token/generate"
)

// NewCommand returns a new cobra.Command for token management
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage one of [generate]",
		Long:  "Manage one of [generate]",
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
	return cmd
}
