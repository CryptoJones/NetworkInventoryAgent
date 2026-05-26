// Neuromancer is one of two paired inventory agents. It listens for health
// checks on the address specified in its config and monitors its partner
// Wintermute for liveness, freshness, and inventory consistency.
//
// "The sky above the port was the color of television, tuned to a dead channel."
// — William Gibson, Neuromancer (1984)
//
// Usage:
//
//	neuromancer [-config neuromancer.json]
package main

import (
	"os"

	"github.com/Ronin48/NetworkInventoryAgent/cmd/internal/runtime"
)

func main() {
	os.Exit(runtime.Run(runtime.Options{
		Name:              "neuromancer",
		DefaultConfigPath: "neuromancer.json",
		WithWatchdog:      true,
	}))
}
