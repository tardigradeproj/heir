package get

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	clusteragentv1alpha1 "github.com/tardigradeproj/heir/api/clusteragent/v1alpha1"
	"github.com/tardigradeproj/heir/pkg/provision/token"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/duration"
)

type workerJoinTokenGetFlagpole struct {
	Kubeconfig string
	SaveToken  string
}

func workerJoinTokenGetCommand() *cobra.Command {
	flags := &workerJoinTokenGetFlagpole{}
	cmd := &cobra.Command{
		Use:     "workerJoinToken [name]",
		Aliases: []string{"token"},
		Short:   "Get worker join tokens on a tenant cluster",
		Long:    "Get worker join tokens on a tenant cluster",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if flags.SaveToken != "" {
					return fmt.Errorf("--save-token requires a worker join token name")
				}
				tokens, err := token.List(cmd.Context(), token.WithKubeconfig(flags.Kubeconfig))
				if err != nil {
					return err
				}
				printWorkerJoinTokens(os.Stdout, tokens)
				return nil
			}

			name := args[0]
			tok, err := token.Get(cmd.Context(), token.WithName(name), token.WithKubeconfig(flags.Kubeconfig))
			if err != nil {
				return err
			}
			printWorkerJoinTokens(os.Stdout, []clusteragentv1alpha1.WorkerJoinToken{*tok})

			if flags.SaveToken == "" {
				return nil
			}

			data, err := token.FetchSecret(cmd.Context(), token.WithName(name), token.WithKubeconfig(flags.Kubeconfig))
			if err != nil {
				return err
			}
			// This is a bootstrap kubeconfig with client credentials in it — never world/group
			// readable. WriteFile only applies this mode on create, so Chmod explicitly too in
			// case --save-token points at a pre-existing, more loosely-permissioned file.
			if err := os.WriteFile(flags.SaveToken, data, 0o600); err != nil {
				return fmt.Errorf("failed to write worker join token to %s: %w", flags.SaveToken, err)
			}
			if err := os.Chmod(flags.SaveToken, 0o600); err != nil {
				return fmt.Errorf("failed to set permissions on %s: %w", flags.SaveToken, err)
			}
			fmt.Printf("wrote worker join token %q to %s\n", name, flags.SaveToken)
			return nil
		},
	}
	cmd.Flags().StringVar(
		&flags.Kubeconfig,
		"kubeconfig",
		"",
		"sets kubeconfig path of the tenant cluster (not the management cluster) instead of "+
			"$KUBECONFIG or $HOME/.kube/config",
	)
	cmd.Flags().StringVarP(
		&flags.SaveToken,
		"save-token",
		"o",
		"",
		"save the named worker join token (a bootstrap kubeconfig) to this path; requires a name",
	)
	return cmd
}

func printWorkerJoinTokens(w io.Writer, tokens []clusteragentv1alpha1.WorkerJoinToken) {
	if len(tokens) == 0 {
		fmt.Fprintln(w, "No worker join tokens found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "NAME\tREADY\tREASON\tEXPIRES\tAGE")
	for _, tok := range tokens {
		ready, reason := "Unknown", ""
		if cond := meta.FindStatusCondition(tok.Status.Conditions, "Ready"); cond != nil {
			ready, reason = string(cond.Status), cond.Reason
		}

		expires := "<none>"
		if tok.Status.ExpiresAt != nil {
			expires = tok.Status.ExpiresAt.Format(time.RFC3339)
		}

		age := duration.ShortHumanDuration(time.Since(tok.CreationTimestamp.Time))
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", tok.Name, ready, reason, expires, age)
	}
}
