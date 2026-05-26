// Agent is a standalone network inventory agent that scans configured subnets
// and persists discovered hosts to SQLite. Unlike the paired Wintermute/
// Neuromancer agents, this binary does not require a watchdog peer.
//
// Usage:
//
//	agent [-config config.json]
package main

import (
	"os"

	"github.com/Ronin48/NetworkInventoryAgent/cmd/internal/runtime"
)

func main() {
	os.Exit(runtime.Run(runtime.Options{
		Name:              "agent",
		DefaultConfigPath: "config.json",
		WithWatchdog:      false,
	}))
}
