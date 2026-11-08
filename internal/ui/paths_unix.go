//go:build !windows

package ui

// foldPathCase is false off Windows, where paths are case-sensitive.
const foldPathCase = false
