package ui

import (
	"runtime"
	"testing"
)

// TestWindowsFileURL checks the Windows path conversion. It is a pure function, so it runs on every OS. The UNC
// inputs are spelled with \x5c escapes, the one backslash.
func TestWindowsFileURL(t *testing.T) {
	const bs = "\x5c"
	unc := bs + bs
	tests := []struct{ name, path, want string }{
		{"drive path with spaces", `C:\Users\Me\My Docs\a b.txt`, "file:///C:/Users/Me/My%20Docs/a%20b.txt"},
		{"drive root", `C:\`, "file:///C:/"},
		{"drive without separator", `C:`, "file:///C:/"},
		{"drive folder with trailing separator", `D:\Photos\`, "file:///D:/Photos/"},
		{"drive lower case letter", `d:\x\y.png`, "file:///d:/x/y.png"},
		{"UNC path", unc + `server\share\dir\f.txt`, "file://server/share/dir/f.txt"},
		{"UNC share root", unc + `server\share`, "file://server/share"},
		{"UNC with spaces", unc + `nas\Team Files\a b.pdf`, "file://nas/Team%20Files/a%20b.pdf"},
		// Non-ASCII characters stay raw: the Windows shell does not decode UTF-8 percent-escapes.
		{"non-ASCII drive path", `C:\Users\Me\été.txt`, "file:///C:/Users/Me/été.txt"},
		{"CJK and a space", `C:\Docs\日本語 ファイル.txt`, "file:///C:/Docs/日本語%20ファイル.txt"},
		{"UNC non-ASCII", unc + `srv\share\dir name\été.txt`, "file://srv/share/dir%20name/été.txt"},
		{"forward slashes", `C:/Users/Me/a.txt`, "file:///C:/Users/Me/a.txt"},
		{"question mark and hash", `C:\a\b?c#d.txt`, "file:///C:/a/b%3Fc%23d.txt"},
		{"unix path falls through", "/home/me/a b.txt", "file:///home/me/a%20b.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowsFileURL(tt.path); got != tt.want {
				t.Errorf("windowsFileURL(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestUnixFileURLUnchanged checks that Linux paths keep the URL they had before the Windows change.
func TestUnixFileURLUnchanged(t *testing.T) {
	if got := unixFileURL("/home/me/a b.txt"); got != "file:///home/me/a%20b.txt" {
		t.Errorf("unixFileURL = %q", got)
	}
	if got := fileURL("/home/me/a b.txt"); got != "file:///home/me/a%20b.txt" {
		t.Errorf("fileURL(unix path) = %q", got)
	}
}

// TestFileURLOnWindows checks that fileURL uses the Windows conversion on Windows.
func TestFileURLOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the drive-letter conversion is used on Windows only")
	}
	if got := fileURL(`C:\Users\Me\My Docs\a b.txt`); got != "file:///C:/Users/Me/My%20Docs/a%20b.txt" {
		t.Errorf("fileURL = %q", got)
	}
}
