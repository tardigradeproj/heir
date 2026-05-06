package main

import (
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tardigrade-runtime/samaritano/pkg/cmd/build"
	"github.com/tardigrade-runtime/samaritano/pkg/cmd/provision"
	"github.com/tardigrade-runtime/samaritano/pkg/cmd/token"
)

func init() {
	log.SetFormatter(&log.TextFormatter{
		ForceColors:     true,
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
}

func main() {
	root := &cobra.Command{
		Use:   "tardigrade",
		Short: "tardigrade is a tool for managing local Kubernetes clusters",
		Long:  "tardigrade creates and manages local Kubernetes clusters using Docker container for control plane and Bare Metal for worker nodes",
	}

	root.AddCommand(build.NewCommand())
	root.AddCommand(token.NewCommand())
	root.AddCommand(provision.NewCommand())
	root.AddCommand(provision.WorkerRunCommand())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
