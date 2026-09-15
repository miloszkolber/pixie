//go:build !linux

package diagnostics

// OwnerLockHeld is unavailable on non-Linux hosts because Pixie's owner lock is
// a Linux advisory lock. Returning nil keeps "not checked" distinct from free.
func OwnerLockHeld(string) *bool { return nil }
