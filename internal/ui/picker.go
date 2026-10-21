package ui

import (
	"context"

	"github.com/egoist/mygo"
)

// pickFolder shows the desktop's directory dialog (MyGo's Dialog.Open with Directory set) and returns the folder
// the user chose, or "" when they cancel.
func pickFolder(context.Context) (string, error) {
	dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Add folder", ButtonLabel: "Add folder", Directory: true})
	if err != nil || len(dirs) == 0 {
		return "", err
	}
	return dirs[0], nil
}
