package theme

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// palette returns a valid colors.toml whose accent is the given color.
func palette(accent string) []byte {
	return []byte(fmt.Sprintf("mode = \"dark\"\naccent = %q\nbackground = \"#111c18\"\nlighter_background = \"#23372b\"\nforeground = \"#c1c497\"\nmuted = \"#53685b\"\n", accent))
}

func accentOf(t *testing.T, s *omarchy) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.kv["accent"]
}

func emitsOf(s *omarchy) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emits
}

// settle waits past the debounce and the retries, then checks that exactly one emit happened
// since before, and that the accent is want.
func settle(t *testing.T, s *omarchy, before int, want string) {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	if got := emitsOf(s) - before; got != 1 {
		t.Errorf("emits after swap = %d, want exactly 1", got)
	}
	if got := accentOf(t, s); got != want {
		t.Errorf("accent = %q, want %q", got, want)
	}
}

func TestRetryRead(t *testing.T) {
	ctx := context.Background()
	calls := 0
	ok := func() (map[string]string, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("missing mid-swap")
		}
		return map[string]string{"accent": "#000000"}, nil
	}
	if _, err := retryRead(ctx, ok, time.Millisecond, 3); err != nil || calls != 3 {
		t.Fatalf("two failures then success: err %v, calls %d", err, calls)
	}
	calls = 0
	always := func() (map[string]string, error) { calls++; return nil, errors.New("gone") }
	if _, err := retryRead(ctx, always, time.Millisecond, 3); err == nil || calls != 4 {
		t.Fatalf("always failing: err %v, calls %d; want an error after 4 attempts", err, calls)
	}
}

func TestWatchRereadsAfterEnrollment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "theme"), 0o755))
	path := filepath.Join(dir, "theme", "colors.toml")
	must(t, os.WriteFile(path, palette("#aaaaaa"), 0o644))
	s := &omarchy{dir: dir, changed: make(chan struct{}, 1)}
	if kv, err := readOmarchy(dir); err != nil {
		t.Fatal(err)
	} else {
		s.kv = kv
	}
	// Swap after the initial read but before the watch goroutine enrolls.
	must(t, os.WriteFile(path, palette("#bbbbbb"), 0o644))
	go s.watch(ctx)
	select {
	case <-s.Changed():
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not emit the palette present at enrollment")
	}
	if got := accentOf(t, s); got != "#bbbbbb" {
		t.Fatalf("accent = %q, want #bbbbbb", got)
	}
}

func TestSwapStylesEmitOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("watcher test waits for debounce")
	}
	t.Run("real folder rm -rf and mv", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dir := t.TempDir()
		must(t, os.Mkdir(filepath.Join(dir, "theme"), 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "theme", "colors.toml"), palette("#aaaaaa"), 0o644))
		s := newOmarchy(ctx, dir)
		time.Sleep(500 * time.Millisecond)
		for _, next := range []string{"#bbbbbb", "#cccccc"} {
			before := emitsOf(s)
			must(t, os.Mkdir(filepath.Join(dir, "next-theme"), 0o755))
			must(t, os.WriteFile(filepath.Join(dir, "next-theme", "colors.toml"), palette(next), 0o644))
			must(t, os.RemoveAll(filepath.Join(dir, "theme")))
			must(t, os.Rename(filepath.Join(dir, "next-theme"), filepath.Join(dir, "theme")))
			must(t, os.WriteFile(filepath.Join(dir, "theme.name"), []byte("x\n"), 0o644))
			settle(t, s, before, next)
		}
	})

	t.Run("symlink ln -nsf", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dir := t.TempDir()
		for _, n := range []string{"a", "b", "c"} {
			must(t, os.MkdirAll(filepath.Join(dir, "themes", n), 0o755))
		}
		must(t, os.WriteFile(filepath.Join(dir, "themes", "a", "colors.toml"), palette("#aaaaaa"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "themes", "b", "colors.toml"), palette("#bbbbbb"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "themes", "c", "colors.toml"), palette("#cccccc"), 0o644))
		must(t, os.Symlink(filepath.Join("themes", "a"), filepath.Join(dir, "theme")))
		s := newOmarchy(ctx, dir)
		time.Sleep(500 * time.Millisecond)
		for _, step := range []struct{ target, accent string }{{"b", "#bbbbbb"}, {"c", "#cccccc"}} {
			before := emitsOf(s)
			// ln -nsf is remove then symlink here, which is not atomic.
			must(t, os.Remove(filepath.Join(dir, "theme")))
			must(t, os.Symlink(filepath.Join("themes", step.target), filepath.Join(dir, "theme")))
			settle(t, s, before, step.accent)
		}
	})

	t.Run("in-place edit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dir := t.TempDir()
		must(t, os.Mkdir(filepath.Join(dir, "theme"), 0o755))
		must(t, os.WriteFile(filepath.Join(dir, "theme", "colors.toml"), palette("#aaaaaa"), 0o644))
		s := newOmarchy(ctx, dir)
		time.Sleep(500 * time.Millisecond)
		before := emitsOf(s)
		// WriteFile truncates and writes: two steps the debounce must merge.
		must(t, os.WriteFile(filepath.Join(dir, "theme", "colors.toml"), palette("#dddddd"), 0o644))
		settle(t, s, before, "#dddddd")
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A malformed file keeps the last good palette: the reload fails and nothing is emitted.
func TestFailedReloadKeepsLastGoodPalette(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "theme"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "theme", "colors.toml"), palette("#aaaaaa"), 0o644))
	s := newOmarchy(ctx, dir)
	before := emitsOf(s)
	must(t, os.WriteFile(filepath.Join(dir, "theme", "colors.toml"), []byte("accent = \"#ff0000\" BROKEN\n"), 0o644))
	s.reload(ctx, time.Millisecond)
	if got := accentOf(t, s); got != "#aaaaaa" {
		t.Errorf("accent after failed reload = %q, want the last good #aaaaaa", got)
	}
	if got := emitsOf(s) - before; got != 0 {
		t.Errorf("emits after failed reload = %d, want 0", got)
	}
}
