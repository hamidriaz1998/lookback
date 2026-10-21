package app

import (
	"context"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
	lrt "github.com/hamidriaz1998/lookback/internal/runtime"
)

// TestOpenServicesWiresRealDeps builds the real services on an empty data folder. Nothing is downloaded and no
// model runs: the test only checks that every Deps field is set and that folder adds and removes reach the
// store through the indexer.
func TestOpenServicesWiresRealDeps(t *testing.T) {
	if testing.Short() {
		t.Skip("opens the store and the indexer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc, err := OpenServices(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenServices: %v", err)
	}
	defer svc.Close()

	d := svc.Deps
	if d.Store == nil || d.Indexer == nil || d.Runtime == nil || d.Lab == nil || d.Downloader == nil ||
		d.Detector == nil || d.Catalog == nil || d.Media == nil || len(d.Themes) == 0 || d.DataDir == "" {
		t.Fatalf("Deps has an unset field: %+v", d)
	}
	if svc.Config.Hotkey != "Super+." || !svc.Config.PauseOnBattery {
		t.Fatalf("Config = %+v, want the SPEC defaults on an empty settings table", svc.Config)
	}
	if st := d.Runtime.Status().State; st != contract.RuntimeStopped {
		t.Fatalf("runtime state = %s, want stopped before any use", st)
	}
	if _, err := d.Catalog.Assets(d.OS, contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}); err != nil {
		t.Fatalf("catalog has no CPU BF16 combination: %v", err)
	}

	dir := t.TempDir()
	f := &contract.Folder{Path: dir}
	if err := d.Store.SaveFolder(ctx, f); err != nil {
		t.Fatalf("SaveFolder: %v", err)
	}
	if f.ID == 0 {
		t.Fatal("SaveFolder did not set an ID through the indexer")
	}
	fs, err := d.Store.ListFolders(ctx)
	if err != nil || len(fs) != 1 || fs[0].Path != dir {
		t.Fatalf("ListFolders = %v, %v; want the added folder", fs, err)
	}
	if err := d.Store.DeleteFolder(ctx, f.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if fs, _ := d.Store.ListFolders(ctx); len(fs) != 0 {
		t.Fatalf("folders after delete = %v, want none", fs)
	}
}

// TestFirstRunStartsWithChosenSpace checks that a clean first run does not start the indexer until the chosen
// combination is saved and installed, and that the first generation takes that combination's space.
func TestFirstRunStartsWithChosenSpace(t *testing.T) {
	if testing.Short() {
		t.Skip("opens the store and the indexer")
	}
	old := newRuntime
	newRuntime = func(cfg lrt.Config) contract.Runtime { return fake.NewRuntime(cfg.Life) }
	defer func() { newRuntime = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc, err := OpenServices(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenServices: %v", err)
	}
	defer svc.Close()

	gens, err := svc.Deps.Store.ListGenerations(ctx)
	if err != nil || len(gens) != 0 {
		t.Fatalf("clean first run: generations = %v, %v; want none before the choice", gens, err)
	}
	q8 := contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0}
	if err := svc.Deps.Store.SetSetting(ctx, "onboard_combo", q8.ID()); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := svc.Deps.Store.SaveCombo(ctx, contract.Installed{Combo: q8, InstalledAt: time.Now()}); err != nil {
		t.Fatalf("SaveCombo: %v", err)
	}
	svc.startIndexer() // the install event (recordInstalls) runs this too

	gens, err = svc.Deps.Store.ListGenerations(ctx)
	if err != nil || len(gens) != 1 {
		t.Fatalf("after install: generations = %v, %v; want one building generation", gens, err)
	}
	if sp := gens[0].Space; sp.TextQuant != contract.QuantQ8_0 || sp.EncQuant != contract.QuantQ8_0 {
		t.Fatalf("first generation space = %+v, want Q8_0/Q8_0", sp)
	}
}
