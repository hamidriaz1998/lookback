package app

import (
	_ "embed"
	"errors"
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
	return Config{Hotkey: "Super+.", PauseOnBattery: true}
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

// ErrNotStarted is returned by ApplySettings before the app starts or after it quits.
var ErrNotStarted = errors.New("app is not running")

// current is the running app. It is set and read on the main thread only.
var current *application

// ApplySettings applies cfg to the running app: it re-registers the hotkey when it changed, and recomputes
// the battery pause at once. It may be called from any goroutine. It returns the hotkey error (nil when the
// new shortcut is active); when registration fails, the previous shortcut stays active.
func ApplySettings(cfg Config) error {
	var err error = ErrNotStarted
	mygo.RunOnMain(func() {
		if current != nil {
			err = current.apply(cfg)
		}
	})
	return err
}

// application holds the state of the running app. Its window, tray and shortcut are used on the main
// thread only, which MyGo runs their callbacks on.
type application struct {
	cfg      Config
	pause    *pauses
	win      *mygo.Window
	tray     *mygo.Tray
	commands *commandRouter // second-instance commands; runs them once the window exists
	hotkey   string         // the accelerator that is registered, "" when none is
	quitting bool           // set before the windows close on a quit, so close-to-tray lets them close
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
	a.commands = &commandRouter{act: func(o Options) {
		if o.Toggle {
			a.toggle()
		} else {
			a.show()
		}
	}}
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		o, err := ParseArgs(args)
		if err != nil {
			log.Printf("second instance: %v", err)
			o = Options{}
		}
		a.commands.receive(o)
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
	a.commands.open()
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

	current = a
	if err := a.setHotkey(a.cfg.Hotkey); err != nil {
		log.Printf("hotkey %q: %v", a.cfg.Hotkey, err) // once per change; Settings shows the bind tip
	}

	mygo.Power.OnSuspend(func() { a.pause.set(contract.PauseSuspend, true) })
	mygo.Power.OnResume(func() { a.resume(mygo.Power.IsOnBattery()) })
	a.checkBattery()
	go func() {
		for range time.Tick(batteryInterval) {
			mygo.RunOnMain(a.checkBattery) // the config is read on the main thread only
		}
	}()
}

// apply takes a settings change on the main thread.
func (a *application) apply(cfg Config) error {
	a.cfg.PauseOnBattery = cfg.PauseOnBattery
	a.checkBattery()
	err := a.setHotkey(cfg.Hotkey)
	if err == nil {
		a.cfg.Hotkey = cfg.Hotkey
	}
	return err
}

// setHotkey makes acc the global shortcut that toggles the window. On failure the previous shortcut is
// registered again, and the attempt is reported through HotkeyStatus.
func (a *application) setHotkey(acc string) error {
	if acc == a.hotkey {
		return nil
	}
	old := a.hotkey
	if old != "" {
		mygo.GlobalShortcut.Unregister(old)
		a.hotkey = ""
	}
	err := mygo.GlobalShortcut.Register(acc, a.toggle)
	if err != nil && old != "" {
		if restoreErr := mygo.GlobalShortcut.Register(old, a.toggle); restoreErr == nil {
			a.hotkey = old
		} else {
			log.Printf("hotkey %q: restoring %q failed: %v", acc, old, restoreErr)
		}
	}
	if err == nil {
		a.hotkey = acc
	}
	setHotkeyStatus(acc, err)
	return err
}

// checkBattery sets the battery pause from the power source. It runs on the main thread.
func (a *application) checkBattery() {
	reconcileBattery(a.pause, a.cfg.PauseOnBattery, mygo.Power.IsOnBattery())
}

// resume lifts the suspend pause after the battery reason is refreshed.
func (a *application) resume(onBattery bool) {
	resumePauses(a.pause, a.cfg.PauseOnBattery, onBattery)
}

// resumePauses sets the battery reason while the suspend reason still holds the indexer, then lifts the
// suspend reason, so the indexer never becomes runnable between the two.
func resumePauses(p *pauses, enabled, onBattery bool) {
	reconcileBattery(p, enabled, onBattery)
	p.set(contract.PauseSuspend, false)
}

// reconcileBattery sets the battery reason from the setting and the power source.
func reconcileBattery(p *pauses, enabled, onBattery bool) {
	p.set(contract.PauseBattery, enabled && onBattery)
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
