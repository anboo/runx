//go:build linux

package process

// fdPathsImpl collects fd targets for every process in the tree on Linux,
// where /proc exposes the descriptors.
func fdPathsImpl(tree []ProcNode) []string {
	var out []string
	for _, n := range tree {
		out = append(out, procFDPaths(n.PID)...)
		if len(out) >= fdPathMax {
			return out[:fdPathMax]
		}
	}
	return out
}