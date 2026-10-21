// Command spike-grid is spike 2 of M0: a MyGo window with a virtualized Grid view of 1,000
// thumbnails. Thumbnails are decoded and downscaled on worker goroutines, and each batch of
// finished bitmaps reaches the view through Window.Update. Run it with: go run ./cmd/spike-grid
package main

import (
	"bufio"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	thumbCount = 1000 // grid items; sources repeat when fewer screenshots exist
	thumbEdge  = 320  // longest edge of a decoded thumbnail, in pixels
	tileWidth  = 180  // minimum tile width in DIPs
	tileHeight = 150  // tile height in DIPs
	winWidth   = 1100 // window width in DIPs, as main opens it
	pad        = 12   // the view's padding on each side, in DIPs
)

// gridColumns is how many columns MyGo's GridView lays out in width DIPs: as many items at least minWidth
// wide, gap apart, as fit (ui.GridView computes floor((width+gap)/(minWidth+gap)), at least one). The grid
// does not expose its column count, so autoscroll computes it from the window width each frame.
func gridColumns(width, minWidth, gap float32) int {
	return max(1, int(math.Floor(float64((width+gap)/(minWidth+gap)))))
}

// decoded is one thumbnail, ready to become a bitmap on the main thread.
type decoded struct {
	index int
	img   *image.RGBA
}

// app is the state the view reads. Only the main thread touches it.
type app struct {
	names  []string
	thumbs []*ui.Bitmap // nil until the thumbnail arrives
	loaded int
	grid   ui.GridState
	start  time.Time
	first  time.Duration // time from start to the first bitmap in the grid
	done   time.Duration // time from start to the last bitmap in the grid

	win  *mygo.Window  // the window, for its size when autoscroll computes the columns
	auto time.Duration // LOOKBACK_AUTOSCROLL: how long to scroll the grid, 0 for none
	row  int           // the grid row autoscroll shows first
	dir  int           // autoscroll direction: 1 down, -1 up
}

// view builds the window from the state, once per frame.
func (a *app) view(c *ui.Context) {
	ui.Column(c).Fill().Padding(12).Gap(8).Children(func() {
		ui.Text(c, fmt.Sprintf("%d of %d thumbnails decoded", a.loaded, thumbCount)).SingleLine()
		ui.GridView(c, &a.grid, thumbCount, tileWidth, tileHeight, func(i int) {
			ui.Column(c).Gap(4).Children(func() {
				if bm := a.thumbs[i]; bm != nil {
					ui.Image(c, bm).Fit(ui.Contain).Grow(1).Radius(8)
				} else {
					ui.Text(c, "Loading").Grow(1)
				}
				ui.Text(c, a.names[i]).SingleLine()
			})
		}).Grow(1)
		if a.auto > 0 && a.loaded == thumbCount {
			a.autoscroll(c)
		}
	})
}

// autoscroll steps the grid one row a frame, reversing at the first and last row, and asks for the
// next frame, so the window redraws at display rate until the timer in load ends the run. One row
// a frame is a harsher case than wheel scrolling. The step is the grid's real column count at the
// window's current width, so each step moves exactly one row.
func (a *app) autoscroll(c *ui.Context) {
	width, _ := a.win.ContentSize()
	cols := gridColumns(float32(width)-2*pad, tileWidth, c.Theme().Space(2))
	rows := (thumbCount + cols - 1) / cols
	if next := a.row + a.dir; next < 0 || next >= rows {
		a.dir = -a.dir
	}
	a.row += a.dir
	a.grid.ScrollTo(a.row*cols, ui.Start)
	c.AnimationFrame()
}

// screenshots returns the PNG files in dir whose names start with "screenshot", sorted.
func screenshots(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(strings.ToLower(name), "screenshot") || !strings.EqualFold(filepath.Ext(name), ".png") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out, nil
}

// decodeThumb decodes the image at path and downscales it so its longest edge is thumbEdge pixels.
// It uses nearest-neighbour sampling, which is enough for a spike.
func decodeThumb(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(bufio.NewReader(f))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if long := max(w, h); long > thumbEdge {
		dw = max(1, w*thumbEdge/long)
		dh = max(1, h*thumbEdge/long)
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := b.Min.Y + y*h/dh
		for x := 0; x < dw; x++ {
			sx := b.Min.X + x*w/dw
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst, nil
}

// load decodes every thumbnail on a worker pool and sends each batch to the main thread.
func (a *app) load(win *mygo.Window, paths []string) {
	jobs := make(chan int)
	results := make(chan decoded, 64)
	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				src := paths[i%len(paths)]
				img, err := decodeThumb(src)
				if err != nil {
					log.Printf("skip %s: %v", src, err)
					results <- decoded{index: i, img: nil}
					continue
				}
				results <- decoded{index: i, img: img}
			}
		}()
	}
	go func() {
		for i := 0; i < thumbCount; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	seen := 0
	for seen < thumbCount {
		batch := []decoded{<-results}
		seen++
	drain:
		for {
			select {
			case d, ok := <-results:
				if !ok {
					break drain
				}
				batch = append(batch, d)
				seen++
			default:
				break drain
			}
		}
		win.Update(func() {
			for _, d := range batch {
				if d.img != nil {
					a.thumbs[d.index] = ui.NewBitmap(d.img)
				}
				a.loaded++
				if a.loaded == 1 {
					a.first = time.Since(a.start)
				}
			}
			if a.loaded == thumbCount {
				a.done = time.Since(a.start)
				log.Printf("decoded %d thumbnails with %d workers: first bitmap %s, all %s",
					thumbCount, runtime.NumCPU(), a.first.Round(time.Millisecond), a.done.Round(time.Millisecond))
				if a.auto > 0 {
					time.AfterFunc(a.auto, func() {
						fmt.Println("autoscroll done")
						os.Exit(0)
					})
				}
			}
		})
	}
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	paths, err := screenshots(filepath.Join(home, "Pictures"))
	if err != nil {
		log.Fatal(err)
	}
	if len(paths) == 0 {
		log.Fatal("no screenshot*.png files in ~/Pictures")
	}
	log.Printf("%d source screenshots", len(paths))

	a := &app{names: make([]string, thumbCount), thumbs: make([]*ui.Bitmap, thumbCount), dir: 1}
	if v := os.Getenv("LOOKBACK_AUTOSCROLL"); v != "" {
		secs, err := strconv.ParseFloat(v, 64)
		if err != nil || secs <= 0 {
			log.Fatalf("LOOKBACK_AUTOSCROLL: want seconds above 0, got %q", v)
		}
		a.auto = time.Duration(secs * float64(time.Second))
	}
	for i := range a.names {
		a.names[i] = filepath.Base(paths[i%len(paths)])
	}
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:   "Lookback spike 2: grid",
			Width:   1100,
			Height:  760,
			Content: ui.View(a.view),
		})
		a.win = win
		a.start = time.Now()
		go a.load(win, paths)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
