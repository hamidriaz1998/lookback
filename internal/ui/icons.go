package ui

import (
	"embed"
	"io/fs"

	"github.com/egoist/mygo/ui"
)

// iconFiles holds the Lucide icons of UI guide section 4 and their license (icons/LICENSE.lucide).
//
//go:embed icons
var iconFiles embed.FS

// icons maps each icon name to its parsed SVG. Parsing happens once, at start.
var icons = func() map[string]*ui.SVG {
	names := []string{
		"search", "folder", "folder-plus", "cpu", "activity", "settings", "image", "file-text",
		"file-spreadsheet", "presentation", "external-link", "copy", "sparkles", "pause", "play",
		"refresh-cw", "trash-2", "triangle-alert", "check", "x", "download",
	}
	m := make(map[string]*ui.SVG, len(names))
	for _, n := range names {
		data, err := fs.ReadFile(iconFiles, "icons/"+n+".svg")
		if err != nil {
			panic(err) // the icon is embedded at build time; a missing one is a build error
		}
		m[n] = ui.MustParseSVG(data)
	}
	return m
}()

// icon returns the named Lucide icon.
func icon(name string) *ui.SVG { return icons[name] }
