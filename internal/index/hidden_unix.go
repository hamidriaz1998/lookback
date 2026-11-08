//go:build !windows

package index

// isHidden is always false outside Windows: there, hidden means a dot prefix, which excludedPath checks itself.
func isHidden(string) bool { return false }
