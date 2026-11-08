package ui

// platformFileURL converts a path of the desktop's file system to a file: URL.
func platformFileURL(p string) string { return windowsFileURL(p) }
