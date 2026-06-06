package version

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/buildinfo"
)

func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Version:   %s\nCommit:    %s\nBuildTime: %s\n",
				buildinfo.Version, buildinfo.CommitID, buildinfo.BuildTime)
		},
	}
}
