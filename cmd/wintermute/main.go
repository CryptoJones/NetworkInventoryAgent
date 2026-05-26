// Wintermute is one of two paired inventory agents. It listens for health
// checks on the address specified in its config and monitors its partner
// Neuromancer for liveness, freshness, and inventory consistency.
//
// Usage:
//
//	wintermute [-config wintermute.json]
package main

import (
	"os"

	"github.com/Ronin48/NetworkInventoryAgent/cmd/internal/runtime"
)

func main() {
	os.Exit(runtime.Run(runtime.Options{
		Name:              "wintermute",
		DefaultConfigPath: "wintermute.json",
		WithWatchdog:      true,
	}))
}
