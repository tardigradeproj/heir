package main

import (
	"os"

	"github.com/tardigradeproj/heir/cmd/tunnel"
)

func main() {
	if err := tunnel.Cmd().Execute(); err != nil {
		os.Exit(1)
	}
}
