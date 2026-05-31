package controlplaneimage

import (
	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/build/controlplaneimage"
	"sigs.k8s.io/kind/pkg/errors"
)

type flagpole struct {
	Source    string
	BuildType string
	Image     string
	BaseImage string
	Arch      string
}

// NewCommand returns a new cobra.Command for building the node image
func NewCommand() *cobra.Command {
	flags := &flagpole{}
	cmd := &cobra.Command{
		Args:  cobra.MaximumNArgs(1),
		Use:   "control-plane-image [kubernetes-source]",
		Short: "Build the control plane node image",
		Long:  "Build the control plane node image which contains Kubernetes build artifacts and other kind requirements",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runE(flags, args)
		},
	}
	cmd.Flags().StringVar(
		&flags.BuildType,
		"type",
		"",
		"optionally specify one of 'file' as the type of build",
	)
	cmd.Flags().StringVar(
		&flags.Image,
		"image",
		controlplaneimage.DefaultImage,
		"name:tag of the resulting image to be built",
	)
	cmd.Flags().StringVar(
		&flags.BaseImage,
		"base-image",
		controlplaneimage.DefaultBaseImage,
		"name:tag of the base image to use for the build",
	)
	cmd.Flags().StringVar(
		&flags.Arch,
		"arch",
		"",
		"architecture to build for, defaults to the host architecture",
	)
	return cmd
}

func runE(flags *flagpole, args []string) error {
	sourceSpec := ""
	if len(args) > 0 {
		sourceSpec = args[0]
	}
	if err := controlplaneimage.Build(
		controlplaneimage.WithImage(flags.Image),
		controlplaneimage.WithBaseImage(flags.BaseImage),
		controlplaneimage.WithKubeParam(sourceSpec),
		controlplaneimage.WithArch(flags.Arch),
		controlplaneimage.WithBuildType(flags.BuildType),
	); err != nil {
		return errors.Wrap(err, "error building control plane image")
	}
	return nil
}
