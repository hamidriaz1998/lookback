package app

import (
	"flag"
	"io"
)

// Options are the command-line flags of lookback.
type Options struct {
	Toggle  bool   // --toggle: show or hide the running window, then exit
	DataDir string // --data-dir: the data folder; empty means the SPEC section 2 default
}

// ParseArgs reads the flags of one command line (without the program name). A second instance
// passes its own command line to the running one (MyGo's single instance lock), so the running
// instance parses the second one's arguments with the same function.
func ParseArgs(args []string) (Options, error) {
	fs := flag.NewFlagSet("lookback", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	toggle := fs.Bool("toggle", false, "show or hide the running window")
	dataDir := fs.String("data-dir", "", "data folder (default $XDG_DATA_HOME/lookback)")
	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}
	return Options{Toggle: *toggle, DataDir: *dataDir}, nil
}
