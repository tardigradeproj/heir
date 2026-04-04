package build

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/tardigrade-runtime/samaritano/pkg/cmd/build/controlplaneimage"
)

// NewCommand returns a new cobra.Command for building
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build one of [control-plane-image]",
		Long:  "Build one of [control-plane-image]",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := cmd.Help()
			if err != nil {
				return err
			}
			return errors.New("subcommand is required")
		},
	}
	// add subcommands
	cmd.AddCommand(controlplaneimage.NewCommand())
	return cmd
}
