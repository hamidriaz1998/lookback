package app

import (
	_ "embed"
	"log"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

const (
	appName         = "Lookback"
	batteryInterval = 30 * time.Second // MyGo reports no battery change event, so the app polls
)

//go:embed resources/tray.png
var trayIcon []byte

// Config holds the settings the app starts with. SPEC section 14 defines the defaults; WP9 reads the
// values from the settings table and passes them here.
type Config struct {
	Hotkey         string // accelerator for the global shortcut that toggles the window
	PauseOnBattery bool   // pause indexing while the computer runs on battery
}

// DefaultConfig returns the SPEC section 14 defaults.
func DefaultConfig() Config {
	return Config{Hotkey: "Super+Shift+Space", PauseOnBattery: true}
}

var hotkeyState struct {
	sync.Mutex
	accelerator string
	err         error
}

func setHotkeyStatus(acc string, err error) {
	hotkeyState.Lock()
	defer hotkeyState.Unlock()
	hotkeyState.accelerator, hotkeyState.err = acc, err
}

// HotkeyStatus returns the accelerator the app tried to register and the registration error, nil when it
// is active. Settings (WP6) reads it to show the bind tip when the portal shortcut is unavailable.
func HotkeyStatus() (accelerator string, err error) {
	hotkeyState.Lock()
	defer hotkeyState.Unlock()
	return hotkeyState.accelerator, hotkeyState.err
}

// application holds the state of the running app. Its window, tray and shortcut are used on the main
// thread only, which MyGo runs their callbacks on.
type application struct {
	cfg      Config
	pause    *pauses
	win      *mygo.Window
	tray     *mygo.Tray
	quitting bool // set before the windows close on a quit, so close-to-tray lets them close
}

// Run starts the app and blocks until it quits. A second instance hands its command line to the
// running one and returns at once (MyGo's single instance lock). idx is the indexer that pauses
// apply to; nil leaves the pause reasons tracked but unused until WP9 wires the indexer.
func Run(opts Options, cfg Config, idx contract.Indexer) error {
	mygo.App.SetName(appName)
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	a := &application{cfg: cfg, pause: newPauses(pauseTarget(idx))}
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		o, err := ParseArgs(args)
		if err != nil {
			log.Printf("second instance: %v", err)
			a.show()
			return
		}
		if o.Toggle {
			a.toggle()
		} else {
			a.show()
		}
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	// The window hides instead of closing, so the app keeps running in the tray (SPEC section 13).
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.WhenReady(a.start)
	return mygo.App.Run()
}

// pauseTarget returns idx as a pauser, or nil when there is no indexer.
func pauseTarget(idx contract.Indexer) pauser {
	if idx == nil {
		return nil
	}
	return idx
}

// start builds the window, the tray, the shortcut and the power hooks once the app is ready.
func (a *application) start() {
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:   appName,
		Width:   960,
		Height:  640,
		Content: ui.View(a.view),
	})
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting {
			e.PreventDefault()
			a.win.Hide()
		}
	})

	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon,
		IconIsTemplate: true,
		ToolTip:        appName,
		Menu:           a.trayMenu(),
	})
	if err != nil {
		log.Printf("tray: %v", err) // the window, hotkey and --toggle still work
	} else {
		a.tray = tray
	}

	err = mygo.GlobalShortcut.Register(a.cfg.Hotkey, a.toggle)
	if err != nil {
		log.Printf("hotkey %q: %v", a.cfg.Hotkey, err) // once, at start; Settings shows the bind tip
	}
	setHotkeyStatus(a.cfg.Hotkey, err)

	mygo.Power.OnSuspend(func() { a.pause.set(contract.PauseSuspend, true) })
	mygo.Power.OnResume(func() {
		a.pause.set(contract.PauseSuspend, false)
		a.checkBattery()
	})
	a.checkBattery()
	go func() {
		for range time.Tick(batteryInterval) {
			a.checkBattery()
		}
	}()
}

// checkBattery sets the battery pause from the power source. It is safe from any goroutine.
func (a *application) checkBattery() {
	a.pause.set(contract.PauseBattery, a.cfg.PauseOnBattery && mygo.Power.IsOnBattery())
}

// show shows and focuses the window.
func (a *application) show() {
	a.win.Show()
}

// toggle shows the window when it is hidden and hides it when it is shown.
func (a *application) toggle() {
	if a.win.IsVisible() {
		a.win.Hide()
		return
	}
	a.win.Show()
}

// trayMenu builds the tray menu. Its pause item names the action that the click performs.
func (a *application) trayMenu() *mygo.Menu {
	pauseLabel := "Pause indexing"
	if a.pause.has(contract.PauseUser) {
		pauseLabel = "Resume indexing"
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open Lookback", Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }},
		{Label: pauseLabel, Click: func(*mygo.MenuItem, *mygo.Window) {
			a.pause.set(contract.PauseUser, !a.pause.has(contract.PauseUser))
			if a.tray != nil {
				a.tray.SetMenu(a.trayMenu())
			}
		}},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
}

// view is the placeholder window. WP6 and WP9 replace it with the search UI.
func (a *application) view(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(8).Children(func() {
		ui.Text(c, appName).FontSize(28).Bold()
		ui.Text(c, "Placeholder window. The search UI arrives with WP6 and WP9.")
	})
}
