package generate

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tardigrade-runtime/samaritano/pkg/token"
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
			t, err := token.CreateBootstrapToken(flags.Kubeconfig, flags.Name, flags.Expiry)
			if err != nil {
				return err
			}
			command := fmt.Sprintf("samaritano worker --token %s.%s --discovery-token-ca-cert-hash %s", t.ID, t.Secret, t.CAHash)
			fmt.Println(command)
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
		"samaritano",
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
