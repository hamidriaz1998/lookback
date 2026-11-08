package app

import "testing"

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		toggle bool
		bg     bool
		err    bool
	}{
		{args: nil},
		{args: []string{"--toggle"}, toggle: true},
		{args: []string{"-toggle"}, toggle: true},
		{args: []string{"--background"}, bg: true},
		{args: []string{"--background", "--toggle"}, toggle: true, bg: true},
		{args: []string{"--bogus"}, err: true},
	} {
		o, err := ParseArgs(tc.args)
		if (err != nil) != tc.err {
			t.Fatalf("ParseArgs(%v) error = %v, want error %v", tc.args, err, tc.err)
		}
		if o.Toggle != tc.toggle || o.Background != tc.bg {
			t.Fatalf("ParseArgs(%v) = toggle %v, background %v; want %v, %v", tc.args, o.Toggle, o.Background, tc.toggle, tc.bg)
		}
	}
}
