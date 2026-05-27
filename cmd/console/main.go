// Console is a graphical terminal UI for the network inventory agent database.
// It opens the SQLite database in read-only mode and provides interactive
// views for hosts, ports, and scan history — mirroring the web admin console.
//
// Usage:
//
//	console [-db inventory.db]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Ronin48/NetworkInventoryAgent/cmd/console/tui"
	"github.com/Ronin48/NetworkInventoryAgent/cmd/internal/runtime"
	"github.com/Ronin48/NetworkInventoryAgent/internal/sqlite"
)

func main() {
	os.Exit(run())
}

// run holds the real entry-point logic so the os.Exit lives in main —
// otherwise deferred cleanup (signal stop, db.Close) would be skipped on
// the error paths (gocritic exitAfterDefer).
func run() int {
	dbPath := flag.String("db", "inventory.db", "path to SQLite database file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("console %s\n", runtime.VersionString())
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := sqlite.Open(ctx, *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "console: open database %q: %v\n", *dbPath, err)
		return 1
	}
	defer func() { _ = db.Close() }()

	m := tui.New(ctx, db.Hosts(), db.Ports(), db.Scans())
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "console: %v\n", err)
		return 1
	}
	return 0
}
