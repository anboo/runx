//go:build !linux

package process

// fdPathsImpl is a no-op on platforms without /proc (macOS, Windows).
func fdPathsImpl(tree []ProcNode) []string {
	return nil
}