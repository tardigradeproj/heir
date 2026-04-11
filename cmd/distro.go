package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/tardigrade-runtime/samaritano/pkg/cmd/build"
	"github.com/tardigrade-runtime/samaritano/pkg/cmd/token"
)

func main() {
	root := &cobra.Command{
		Use:   "tardigrade",
		Short: "tardigrade is a tool for managing local Kubernetes clusters",
		Long:  "tardigrade creates and manages local Kubernetes clusters using Docker container for control plane and Bare Metal for worker nodes",
	}

	root.AddCommand(build.NewCommand())
	root.AddCommand(token.NewCommand())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
