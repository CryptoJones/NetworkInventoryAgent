//go:build !darwin && !windows

package scanner

// neighbourMAC is a no-op on platforms without a dedicated implementation.
// Linux resolves neighbours through parseProcARP (the /proc table) in
// lookupARP, so its fallback is empty; macOS (arp_darwin.go) and Windows
// (arp_windows.go) have native lookups. Anything else degrades gracefully to
// vendor-less inventory rather than guessing.
func neighbourMAC(string) string { return "" }
