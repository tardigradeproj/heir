package get

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	controlplanev1alpha1 "github.com/tardigradeproj/heir/api/controlplane/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/controlplane"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/duration"
)

type runtimeClusterGetFlagpole struct {
	Namespace      string
	Kubeconfig     string
	Server         string
	SaveKubeconfig string
}

func runtimeClusterGetCommand() *cobra.Command {
	flags := &runtimeClusterGetFlagpole{}
	cmd := &cobra.Command{
		Use:     "runtimeCluster [name]",
		Aliases: []string{"cluster"},
		Short:   "Get control plane clusters on the management cluster",
		Long:    "Get control plane clusters on the management cluster",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if flags.SaveKubeconfig != "" {
					return fmt.Errorf("--save-kubeconfig requires a runtime cluster name")
				}
				runtimes, err := controlplane.List(cmd.Context(),
					controlplane.WithNamespace(flags.Namespace),
					controlplane.WithKubeconfig(flags.Kubeconfig),
				)
				if err != nil {
					return err
				}
				printRuntimeClusters(os.Stdout, runtimes)
				return nil
			}

			name := args[0]
			rt, err := controlplane.Get(cmd.Context(),
				controlplane.WithName(name),
				controlplane.WithNamespace(flags.Namespace),
				controlplane.WithKubeconfig(flags.Kubeconfig),
			)
			if err != nil {
				return err
			}
			printRuntimeClusters(os.Stdout, []controlplanev1alpha1.Runtime{*rt})

			if flags.SaveKubeconfig == "" {
				return nil
			}

			data, err := controlplane.FetchKubeconfig(cmd.Context(),
				controlplane.WithName(name),
				controlplane.WithNamespace(flags.Namespace),
				controlplane.WithKubeconfig(flags.Kubeconfig),
				controlplane.WithServer(flags.Server),
			)
			if err != nil {
				return err
			}
			if err := writeKubeconfigFile(flags.SaveKubeconfig, data); err != nil {
				return err
			}
			fmt.Printf("wrote admin kubeconfig for cluster %q to %s\n", name, flags.SaveKubeconfig)
			return nil
		},
	}
	cmd.Flags().StringVar(
		&flags.Namespace,
		"namespace",
		"default",
		"namespace the control plane was provisioned in",
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
		"override the server URL baked into the saved kubeconfig (e.g. https://127.0.0.1:30080 for local dev)",
	)
	cmd.Flags().StringVarP(
		&flags.SaveKubeconfig,
		"save-kubeconfig",
		"o",
		"",
		"save the named cluster's admin kubeconfig to this path; requires a name",
	)
	return cmd
}

// writeKubeconfigFile saves kubeconfig data to path. The data is an admin kubeconfig with
// client credentials in it — never world/group readable. WriteFile only applies the given mode
// on create, so Chmod explicitly too in case path points at a pre-existing, more
// loosely-permissioned file.
func writeKubeconfigFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("failed to write kubeconfig to %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("failed to set permissions on %s: %w", path, err)
	}
	return nil
}

func printRuntimeClusters(w io.Writer, runtimes []controlplanev1alpha1.Runtime) {
	if len(runtimes) == 0 {
		fmt.Fprintln(w, "No runtime clusters found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "NAME\tAVAILABLE\tREASON\tHOST\tPORT\tAGE")
	for _, rt := range runtimes {
		available, reason := "Unknown", ""
		if cond := meta.FindStatusCondition(rt.Status.Conditions, "Available"); cond != nil {
			available, reason = string(cond.Status), cond.Reason
		}

		endpoint := rt.Spec.Cluster.ControlPlaneExternalEndpoint.APIServer
		age := duration.ShortHumanDuration(time.Since(rt.CreationTimestamp.Time))
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", rt.Name, available, reason, endpoint.Host, endpoint.Port, age)
	}
}
