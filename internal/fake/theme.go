package fake

import "github.com/egoist/mygo/ui"

// ThemeSource is a fixed contract.ThemeSource. Theme returns a copy of the base theme, so the
// fake does not restyle; it is enough for the UI to wire the theme picker.
type ThemeSource struct {
	IDValue, NameValue string
}

// NewThemeSource returns a fixed theme source with the given ID and name.
func NewThemeSource(id, name string) *ThemeSource {
	return &ThemeSource{IDValue: id, NameValue: name}
}

// ID returns the source ID.
func (s *ThemeSource) ID() string { return s.IDValue }

// Name returns the display name.
func (s *ThemeSource) Name() string { return s.NameValue }

// Theme returns a copy of base.
func (s *ThemeSource) Theme(base *ui.Theme) *ui.Theme {
	t := *base
	return &t
}

// Changed returns nil: a fixed theme never changes.
func (s *ThemeSource) Changed() <-chan struct{} { return nil }
