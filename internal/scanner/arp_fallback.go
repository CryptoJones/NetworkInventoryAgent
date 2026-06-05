//go:build !darwin

package scanner

// neighbourMAC is a no-op on every platform except macOS. Linux resolves
// neighbours through parseProcARP (the /proc table) in lookupARP, so its
// fallback is empty; Windows and others have no implementation yet and
// degrade gracefully to vendor-less inventory rather than guessing.
func neighbourMAC(string) string { return "" }
