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

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Ronin48/NetworkInventoryAgent/cmd/console/tui"
	"github.com/Ronin48/NetworkInventoryAgent/internal/sqlite"
)

func main() {
	dbPath := flag.String("db", "inventory.db", "path to SQLite database file")
	flag.Parse()

	db, err := sqlite.Open(context.Background(), *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "console: open database %q: %v\n", *dbPath, err)
		os.Exit(1)
	}
	defer db.Close()

	m := tui.New(db.Hosts(), db.Ports(), db.Scans())
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "console: %v\n", err)
		os.Exit(1)
	}
}
