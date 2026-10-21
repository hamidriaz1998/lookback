// Command lookback-ui runs the Lookback window on the in-memory services of internal/fake, for UI
// development, with thumbnails taken from the screenshots in ~/Pictures. WP9 moves the window into
// cmd/lookback (WP7's app shell) with the real services.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	lookui "github.com/hamidriaz1998/lookback/internal/ui"
)

func main() {
	fake := flag.Bool("fake", true, "run on the in-memory fakes of internal/fake")
	firstRun := flag.Bool("first-run", false, "start with the onboarding flow")
	page := flag.String("page", "search", "sidebar page to open: search, folders, models, activity or settings")
	theme := flag.String("theme", "", "theme to apply at start: light, dark, or empty for the desktop's")
	flag.Parse()
	if !*fake {
		log.Fatal("only -fake is wired until WP9")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deps, err := fakeDeps(ctx, *firstRun, *theme)
	if err != nil {
		log.Fatal(err)
	}
	app := lookui.New(deps)
	app.ShowPage(*page)
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Lookback",
			Width:     1280,
			Height:    800,
			MinWidth:  720,
			MinHeight: 480,
			Content:   ui.View(app.View),
		})
		app.Start(ctx, win.Update)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
