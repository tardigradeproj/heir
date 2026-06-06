package main

import (
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tardigradeproj/heir/pkg/cmd/build"
	"github.com/tardigradeproj/heir/pkg/cmd/provision"
	"github.com/tardigradeproj/heir/pkg/cmd/token"
	cmdversion "github.com/tardigradeproj/heir/pkg/cmd/version"
)

func init() {
	log.SetFormatter(&log.TextFormatter{
		ForceColors:     true,
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	levelStr := os.Getenv("LOG_LEVEL")
	if levelStr == "" {
		levelStr = "info"
	}
	level, err := log.ParseLevel(levelStr)
	if err != nil {
		log.Warnf("invalid LOG_LEVEL %q, defaulting to info", levelStr)
		level = log.InfoLevel
	}
	log.SetLevel(level)
}

func main() {
	root := &cobra.Command{
		Use:   "heir",
		Short: "heir creates and operates kubernetes clusters",
		Long:  "heir creates and operates kubernetes clusters",
	}

	root.AddCommand(build.NewCommand())
	root.AddCommand(token.NewCommand())
	root.AddCommand(provision.NewCommand())
	root.AddCommand(provision.WorkerRunCommand())
	root.AddCommand(cmdversion.NewCommand())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
