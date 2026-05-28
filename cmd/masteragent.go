package main

import (
	"os"

	"github.com/tardigradeproj/heir/cmd/masteragent"
)

// create a routine to setup kubernetes service
// sync manifests
// health check processes
// run kine
func main() {
	if err := masteragent.Cmd().Execute(); err != nil {
		os.Exit(1)
	}
}
