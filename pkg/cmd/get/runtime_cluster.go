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
	Namespace  string
	Kubeconfig string
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

			rt, err := controlplane.Get(cmd.Context(),
				controlplane.WithName(args[0]),
				controlplane.WithNamespace(flags.Namespace),
				controlplane.WithKubeconfig(flags.Kubeconfig),
			)
			if err != nil {
				return err
			}
			printRuntimeClusters(os.Stdout, []controlplanev1alpha1.Runtime{*rt})
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
	return cmd
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
