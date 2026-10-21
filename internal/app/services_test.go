package app

import (
	"context"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
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
