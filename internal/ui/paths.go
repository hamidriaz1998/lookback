package ui

import "strings"

// pathsSame reports whether a and b name the same path. With fold, the comparison ignores case, as Windows does.
func pathsSame(a, b string, fold bool) bool {
	if fold {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// pathHasPrefix reports whether p starts with prefix. With fold, the comparison ignores case.
func pathHasPrefix(p, prefix string, fold bool) bool {
	if len(p) < len(prefix) {
		return false
	}
	if fold {
		return strings.EqualFold(p[:len(prefix)], prefix)
	}
	return strings.HasPrefix(p, prefix)
}
