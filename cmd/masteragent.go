package main

import (
	"os"

	"github.com/tardigrade-runtime/samaritano/cmd/masteragent"
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
