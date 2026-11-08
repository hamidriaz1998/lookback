package ui

import (
	"net/url"
	"strings"
)

// windowsFileURL is the file: URL of a Windows path. A drive path becomes file:///C:/dir/a%20b.txt, a drive root
// becomes file:///C:/, and a UNC path becomes file://server/share/dir/f.txt. Any other path is encoded as a Unix
// path. It is a pure function, so the tests of the Windows cases run on every OS.
func windowsFileURL(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	switch {
	case strings.HasPrefix(p, "//") && !strings.HasPrefix(p, `//?/`) && !strings.HasPrefix(p, `//./`):
		parts := nonEmptyParts(strings.Split(p[2:], "/"))
		if len(parts) == 0 {
			return "file://"
		}
		return "file://" + escapeSegment(parts[0]) + "/" + escapeParts(parts[1:])
	case len(p) >= 2 && p[1] == ':' && isASCIILetter(p[0]):
		parts := nonEmptyParts(strings.Split(p[2:], "/"))
		out := "file:///" + p[:2] + "/" + escapeParts(parts)
		if len(parts) > 0 && strings.HasSuffix(p, "/") {
			out += "/"
		}
		return out
	default:
		return unixFileURL(p)
	}
}

// unixFileURL is the file: URL of a slash-separated path, such as /home/me/a%20b.txt.
func unixFileURL(p string) string {
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// backslash is one \ character. The Windows prefixes below are built from it, so that the source spells each one
// as an escape.
const backslash = "\x5c"

// hasUNCPrefix reports whether p is a Windows UNC path, a share that starts with two separators. The device
// prefixes of the form two separators, ?, and a separator are not UNC paths.
func hasUNCPrefix(p string) bool {
	unc := backslash + backslash
	if strings.HasPrefix(p, unc+"?"+backslash) || strings.HasPrefix(p, unc+"."+backslash) {
		return false
	}
	return strings.HasPrefix(p, unc) || strings.HasPrefix(p, "//")
}

// nonEmptyParts drops the empty parts that come from doubled or trailing separators.
func nonEmptyParts(parts []string) []string {
	out := parts[:0]
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// escapeParts percent-encodes each part as a URL path segment and joins them with /.
func escapeParts(parts []string) string {
	out := make([]string, len(parts))
	for i, s := range parts {
		out[i] = escapeSegment(s)
	}
	return strings.Join(out, "/")
}

// escapeSegment percent-encodes the ASCII characters of a path segment that a URL cannot hold (a space, %, #, ?,
// and so on) and keeps every other character as it is. Non-ASCII characters stay raw on purpose: the Windows shell
// opens file:///C:/dir/été.txt, but it does not decode UTF-8 percent-escapes, so file:///C:/dir/%C3%A9t%C3%A9.txt
// fails with "file not found" (checked with ShellExecuteW on Windows 11).
func escapeSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x80 {
			b.WriteRune(r)
		} else {
			b.WriteString(url.PathEscape(string(r)))
		}
	}
	return b.String()
}

// isASCIILetter reports whether b is a letter A-Z or a-z, the only bytes that start a drive letter.
func isASCIILetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
