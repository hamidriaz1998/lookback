// Command theme-demo shows one theme of Lookback: swatches, text, muted text, an accent button and
// a bordered card, all colored from the theme of the frame. Run it with:
//
//	go run ./cmd/theme-demo --theme one-dark
//
// The IDs are those of the built-in sources, plus "omarchy" when the Omarchy file exists.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/hamidriaz1998/lookback/internal/theme"
)

func main() {
	id := flag.String("theme", "catppuccin-mocha", "theme ID")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sources := theme.Builtins()
	if om := theme.Omarchy(ctx); om != nil {
		sources = append(sources, om)
	}
	mgr, err := theme.NewManager(ctx, sources, *id)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		for _, s := range sources {
			fmt.Fprintln(os.Stderr, "  ", s.ID())
		}
		os.Exit(2)
	}

	view := func(c *ui.Context) {
		// The base is MyGo's default for the OS appearance, as SPEC section 15 says.
		base := ui.LightTheme()
		if mygo.Theme.IsDark() {
			base = ui.DarkTheme()
		}
		t := mgr.Theme(base)
		c.SetTheme(t)
		ui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
			ui.Text(c, mgr.Active().Name()).FontSize(20).Bold()
			ui.Row(c).Gap(8).Children(func() {
				for _, sw := range []struct {
					label string
					col   ui.Color
				}{
					{"Background", t.Background}, {"Surface", t.Surface}, {"Accent", t.Accent},
					{"Danger", t.Danger}, {"Warning", t.Warning}, {"Success", t.Success},
				} {
					ui.Column(c).Gap(4).Children(func() {
						ui.Column(c).Size(88, 44).Background(sw.col).Border(1, t.Border).Radius(t.Radius)
						ui.Text(c, sw.label).FontSize(12).TextColor(t.TextMuted)
					})
				}
			})
			ui.Text(c, "Body text reads on the background.")
			ui.Text(c, "Muted text is for secondary lines.").TextColor(t.TextMuted)
			ui.PrimaryButton(c.Key("accent"), "Accent button").Width(180)
			ui.Column(c).Padding(16).Gap(8).Background(t.Surface).Border(1, t.Border).Radius(t.Radius).Children(func() {
				ui.Text(c, "Card title").Bold()
				ui.Text(c, "A bordered card on the surface color.").TextColor(t.TextMuted)
			})
		})
	}

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "Theme demo: " + *id,
			Width:   520,
			Height:  420,
			Content: ui.View(view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
