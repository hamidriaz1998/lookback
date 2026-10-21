// Package watch runs one recursive folder watcher per folder, with events debounced for 1 s, and the
// startup rescan that compares size and mtime with the files table (SPEC sections 11 and 13).
package watch
