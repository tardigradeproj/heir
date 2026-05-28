package generate

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/token"
)

type flagpole struct {
	Kubeconfig string
	Name       string
	Expiry     time.Duration
}

// NewCommand returns a new cobra.Command for generating a bootstrap token secret
func NewCommand() *cobra.Command {
	flags := &flagpole{}
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a bootstrap token secret on the target cluster",
		Long:  "Generate a bootstrap token secret on the target cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			b64Kubeconfig, err := token.CreateBootstrapToken(cmd.Context(), flags.Kubeconfig, flags.Name, flags.Expiry)
			if err != nil {
				return fmt.Errorf("unable to generate bootstrap token: %w", err)
			}
			fmt.Println(b64Kubeconfig)
			return nil
		},
	}
	cmd.Flags().StringVar(
		&flags.Kubeconfig,
		"kubeconfig",
		"",
		"sets kubeconfig path instead of $KUBECONFIG or $HOME/.kube/config",
	)
	cmd.Flags().StringVar(
		&flags.Name,
		"name",
		"heir",
		"the context name",
	)
	cmd.Flags().DurationVar(
		&flags.Expiry,
		"expiry",
		1*time.Hour,
		"expiration time of the token (e.g. 1.5h, 2h45m, 300ms)",
	)
	return cmd
}
