package app

import "testing"

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		toggle bool
		err    bool
	}{
		{args: nil},
		{args: []string{"--toggle"}, toggle: true},
		{args: []string{"-toggle"}, toggle: true},
		{args: []string{"--bogus"}, err: true},
	} {
		o, err := ParseArgs(tc.args)
		if (err != nil) != tc.err {
			t.Fatalf("ParseArgs(%v) error = %v, want error %v", tc.args, err, tc.err)
		}
		if o.Toggle != tc.toggle {
			t.Fatalf("ParseArgs(%v).Toggle = %v, want %v", tc.args, o.Toggle, tc.toggle)
		}
	}
}
