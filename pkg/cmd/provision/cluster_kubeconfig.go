package provision

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/provision/controlplane"
)

type clusterKubeconfigFlagpole struct {
	Namespace  string
	Kubeconfig string
	Server     string
	Output     string
}

func clusterKubeconfigCommand() *cobra.Command {
	flags := &clusterKubeconfigFlagpole{}
	cmd := &cobra.Command{
		Use:     "clusterKubeconfig <name>",
		Aliases: []string{"kubeconfig"},
		Short:   "Fetch a tenant cluster's admin kubeconfig",
		Long: "Fetch the admin kubeconfig Heir published for a Runtime's tenant cluster, and write " +
			"it to a local file",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			fmt.Printf("Generating kubeconfig for cluster %q...\n", name)
			data, err := controlplane.FetchKubeconfig(cmd.Context(),
				controlplane.WithName(name),
				controlplane.WithNamespace(flags.Namespace),
				controlplane.WithKubeconfig(flags.Kubeconfig),
				controlplane.WithServer(flags.Server),
			)
			if err != nil {
				return err
			}

			output := flags.Output
			if output == "" {
				output = fmt.Sprintf("%s-kubeconfig.yaml", name)
			}
			if err := os.WriteFile(output, data, 0o600); err != nil {
				return fmt.Errorf("failed to write kubeconfig to %s: %w", output, err)
			}
			if err := os.Chmod(output, 0o600); err != nil {
				return fmt.Errorf("failed to set permissions on %s: %w", output, err)
			}

			fmt.Printf("Wrote admin kubeconfig for cluster %q to %q\n", name, output)
			return nil
		},
	}
	cmd.Flags().StringVar(
		&flags.Namespace,
		"namespace",
		"default",
		"namespace the Runtime was provisioned in",
	)
	cmd.Flags().StringVar(
		&flags.Kubeconfig,
		"kubeconfig",
		"",
		"sets kubeconfig path of the management cluster instead of $KUBECONFIG or $HOME/.kube/config",
	)
	cmd.Flags().StringVar(
		&flags.Server,
		"server",
		"",
		"override the server URL baked into the fetched kubeconfig (e.g. https://127.0.0.1:30080 for local dev)",
	)
	cmd.Flags().StringVarP(
		&flags.Output,
		"output",
		"o",
		"",
		"path to write the kubeconfig to (default \"<name>-kubeconfig\")",
	)
	return cmd
}
