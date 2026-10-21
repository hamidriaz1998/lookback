// Command mksample writes the bundled sample set (SPEC section 6, step 2): 8 self-made images of different sizes
// and 16 self-written text chunks. It draws with Go's image packages only, so the output is deterministic and
// has no third-party content. Run it from the repository root: go run ./tools/mksample
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const root = "assets/sample"

func main() {
	for _, d := range []string{"images", "text"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			log.Fatal(err)
		}
	}
	for i, draw := range imageSpecs {
		name := fmt.Sprintf("img-%02d.png", i+1)
		img := draw()
		f, err := os.Create(filepath.Join(root, "images", name))
		if err != nil {
			log.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			log.Fatal(err)
		}
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}
	}
	for i, text := range chunks {
		name := fmt.Sprintf("chunk-%02d.txt", i+1)
		if err := os.WriteFile(filepath.Join(root, "text", name), []byte(text+"\n"), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

var imageSpecs = []func() *image.RGBA{
	chartBars,    // 640x480
	shapes,       // 1024x768
	textCard,     // 320x240
	portraitPage, // 800x1200
	dashboard,    // 1600x900
	circles,      // 480x480
	tableScreen,  // 1280x720
	smallSwatch,  // 256x256
}

func canvas(w, h int, bg color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, bg)
		}
	}
	return img
}

func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func text(img *image.RGBA, x, y int, s string, c color.RGBA, scale int) {
	// basicfont is 7x13 pixels. Scale it up by drawing each glyph on a larger canvas.
	face := basicfont.Face7x13
	if scale <= 1 {
		d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
		d.DrawString(s)
		return
	}
	glyphs := image.NewRGBA(image.Rect(0, 0, len(s)*7+2, 15))
	d := &font.Drawer{Dst: glyphs, Src: image.NewUniform(color.RGBA{255, 255, 255, 255}), Face: face, Dot: fixed.P(0, 12)}
	d.DrawString(s)
	for gy := 0; gy < 15; gy++ {
		for gx := 0; gx < glyphs.Bounds().Dx(); gx++ {
			if glyphs.RGBAAt(gx, gy).A == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetRGBA(x+gx*scale+dx, y+(gy-12)*scale+dy, c)
				}
			}
		}
	}
}

// chartBars: 640x480 bar chart with axes and labels.
func chartBars() *image.RGBA {
	img := canvas(640, 480, color.RGBA{250, 250, 248, 255})
	fillRect(img, image.Rect(70, 40, 74, 420), color.RGBA{60, 60, 60, 255})
	fillRect(img, image.Rect(70, 416, 600, 420), color.RGBA{60, 60, 60, 255})
	vals := []int{120, 210, 175, 300, 260, 340}
	for i, v := range vals {
		x0 := 100 + i*82
		fillRect(img, image.Rect(x0, 416-v, x0+50, 416), color.RGBA{36, 110, 180, 255})
		text(img, x0, 436, fmt.Sprintf("Q%d", i+1), color.RGBA{40, 40, 40, 255}, 1)
	}
	text(img, 90, 30, "Quarterly backups (GB)", color.RGBA{20, 20, 20, 255}, 1)
	return img
}

// shapes: 1024x768, overlapping shapes on a gradient.
func shapes() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 1024, 768))
	for y := 0; y < 768; y++ {
		for x := 0; x < 1024; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x / 4), uint8(y / 4), 200, 255})
		}
	}
	for i := 0; i < 9; i++ {
		cx, cy := 120+i*95, 180+(i%3)*190
		r := 60 + i*6
		c := color.RGBA{uint8(40 * i), uint8(255 - 25*i), uint8(120 + 10*i), 255}
		for y := cy - r; y <= cy+r; y++ {
			for x := cx - r; x <= cx+r; x++ {
				dx, dy := x-cx, y-cy
				if dx*dx+dy*dy <= r*r && x >= 0 && y >= 0 && x < 1024 && y < 768 {
					img.SetRGBA(x, y, c)
				}
			}
		}
	}
	return img
}

// textCard: 320x240, a short error dialog.
func textCard() *image.RGBA {
	img := canvas(320, 240, color.RGBA{236, 236, 236, 255})
	fillRect(img, image.Rect(0, 0, 320, 28), color.RGBA{200, 40, 40, 255})
	text(img, 10, 18, "Error", color.RGBA{255, 255, 255, 255}, 1)
	text(img, 14, 60, "Could not connect to the", color.RGBA{20, 20, 20, 255}, 1)
	text(img, 14, 78, "hotspot. Check the password", color.RGBA{20, 20, 20, 255}, 1)
	text(img, 14, 96, "and try again.", color.RGBA{20, 20, 20, 255}, 1)
	fillRect(img, image.Rect(200, 180, 300, 210), color.RGBA{60, 120, 210, 255})
	text(img, 226, 200, "OK", color.RGBA{255, 255, 255, 255}, 1)
	return img
}

// portraitPage: 800x1200, a page of lines.
func portraitPage() *image.RGBA {
	img := canvas(800, 1200, color.RGBA{255, 253, 245, 255})
	text(img, 60, 80, "Invoice 2026-0412", color.RGBA{10, 10, 10, 255}, 2)
	for i := 0; i < 22; i++ {
		text(img, 60, 150+i*36, fmt.Sprintf("Line item %02d  hosting plan, monthly  %d.00 EUR", i+1, 10+i*3), color.RGBA{50, 50, 50, 255}, 1)
	}
	fillRect(img, image.Rect(60, 1000, 740, 1002), color.RGBA{120, 120, 120, 255})
	text(img, 60, 1040, "Total due: 1,284.00 EUR", color.RGBA{10, 10, 10, 255}, 1)
	return img
}

// dashboard: 1600x900, panels with bars and labels.
func dashboard() *image.RGBA {
	img := canvas(1600, 900, color.RGBA{24, 26, 32, 255})
	for p := 0; p < 6; p++ {
		x0 := 40 + (p%3)*520
		y0 := 40 + (p/3)*420
		fillRect(img, image.Rect(x0, y0, x0+480, y0+380), color.RGBA{40, 44, 54, 255})
		text(img, x0+16, y0+28, fmt.Sprintf("Panel %d", p+1), color.RGBA{230, 230, 230, 255}, 1)
		for i := 0; i < 12; i++ {
			h := 40 + ((i*37 + p*53) % 200)
			fillRect(img, image.Rect(x0+30+i*36, y0+340-h, x0+54+i*36, y0+340), color.RGBA{90, 200, 160, 255})
		}
	}
	return img
}

// circles: 480x480, a ring of discs.
func circles() *image.RGBA {
	img := canvas(480, 480, color.RGBA{20, 20, 30, 255})
	for i := 0; i < 12; i++ {
		a := float64(i) * 2 * math.Pi / 12
		cx, cy := 240+int(150*math.Cos(a)), 240+int(150*math.Sin(a))
		c := color.RGBA{uint8(255 - 15*i), uint8(60 + 12*i), uint8(200 - 8*i), 255}
		for y := cy - 36; y <= cy+36; y++ {
			for x := cx - 36; x <= cx+36; x++ {
				dx, dy := x-cx, y-cy
				if dx*dx+dy*dy <= 36*36 {
					img.SetRGBA(x, y, c)
				}
			}
		}
	}
	return img
}

// tableScreen: 1280x720, a table with a header row.
func tableScreen() *image.RGBA {
	img := canvas(1280, 720, color.RGBA{255, 255, 255, 255})
	cols := []string{"File", "Folder", "Modified", "Size"}
	for c, name := range cols {
		fillRect(img, image.Rect(20+c*300, 20, 20+(c+1)*300-2, 60), color.RGBA{225, 230, 240, 255})
		text(img, 30+c*300, 46, name, color.RGBA{20, 20, 20, 255}, 1)
	}
	for r := 0; r < 14; r++ {
		y := 80 + r*40
		text(img, 30, y+14, fmt.Sprintf("report-%03d.pdf", r*7+3), color.RGBA{30, 30, 30, 255}, 1)
		text(img, 330, y+14, "Documents/Work", color.RGBA{30, 30, 30, 255}, 1)
		text(img, 630, y+14, fmt.Sprintf("2026-%02d-%02d", r%12+1, r%27+1), color.RGBA{30, 30, 30, 255}, 1)
		text(img, 930, y+14, fmt.Sprintf("%d KB", 120+r*41), color.RGBA{30, 30, 30, 255}, 1)
	}
	return img
}

// smallSwatch: 256x256, a color grid.
func smallSwatch() *image.RGBA {
	img := canvas(256, 256, color.RGBA{0, 0, 0, 255})
	for gy := 0; gy < 4; gy++ {
		for gx := 0; gx < 4; gx++ {
			fillRect(img, image.Rect(gx*64+4, gy*64+4, gx*64+60, gy*64+60),
				color.RGBA{uint8(gx * 85), uint8(gy * 85), uint8(255 - gx*40 - gy*20), 255})
		}
	}
	return img
}

// chunks are the 16 text chunks. Each is a short self-written note of about 80 to 120 words.
var chunks = []string{
	"Backup plan for the home server. The nightly job copies the photo library to the external drive at 02:00 and keeps the last thirty snapshots. The weekly job sends a copy to the cloud bucket on Sunday. Check the log every Monday; a failed run shows up as a red line at the end of the summary.",
	"Trip notes from the coast in May. We rented a small car in the port town and drove north along the cliffs. The first stop was the lighthouse, then the fish market, then a long walk to the bay where the seals rest. Pack a rain jacket even when the forecast says sun.",
	"Recipe for lentil soup. Rinse one cup of red lentils. Fry an onion, two carrots and a stick of celery in olive oil for ten minutes. Add cumin, the lentils and five cups of stock. Simmer for twenty-five minutes, blend half of it, and finish with lemon juice and salt.",
	"Meeting summary, project Atlas, Tuesday. The team agreed to freeze the API by the end of the month. Mira will write the migration guide. Omar reported that the load tests pass at two thousand requests per second. The next review is on the third of the month, with the security team.",
	"Hotspot troubleshooting. If the laptop cannot join the phone hotspot, forget the network and join again with the password from the phone settings. Turn off the power saving mode of the wireless card. Restart both devices, and keep them within three metres of each other.",
	"Guitar practice log. Monday: scales for twenty minutes, then the second verse of the new song at half speed. Wednesday: barre chords, which still buzz on the high strings. Friday: recorded a take and listened back. The timing drifts in the bridge, so practise with the metronome at seventy beats.",
	"Insurance renewal reminder. The home policy renews on the first of September. The premium rose by four percent because of the rebuild cost index. Compare two quotes before you accept the renewal, and check that the electronics cover includes laptops taken outside the house.",
	"Garden journal. The tomatoes went into the bed under the south wall after the frost. Water them at the root in the morning and tie the stems to the stakes every ten days. The basil needs more light than the pots on the balcony can give it.",
	"Lease document, section four. The tenant pays the rent on the first day of each month by bank transfer. Late payments after the fifth day carry a fee of twenty euros. Repairs in the flat are reported in writing to the landlord, who answers within ten working days.",
	"Bug report for the sync client. When a folder is renamed while the app is paused, the rename is lost on resume and the old name comes back. Steps: pause, rename the folder in the file manager, resume. Expected: the new name is kept. Logs attached from the crash at 14:02.",
	"Reading notes on distributed systems. A consensus protocol needs a majority of nodes to agree before a value is committed. Leader election uses timeouts with random jitter so that two nodes rarely start an election at the same moment. Read the paper twice; the second time, skip the proofs.",
	"School newsletter. Parents are invited to the spring concert on the twelfth at six in the evening, in the main hall. Tickets are free but limited to four per family. The choir rehearses on Thursdays after lunch, and new singers are welcome at any rehearsal.",
	"Expense report for the conference in Lisbon. Flight, two hundred and ten euros. Hotel for three nights, four hundred and five euros. Train to the venue, twelve euros. Dinner with the partners, ninety euros. Keep the paper receipts; the finance team asks for them within thirty days.",
	"Notes on the new camera. The sensor is larger than the old one, so the depth of field is shallower at the same aperture. Use the focus peaking display for portraits. The battery lasts about four hundred shots in cold weather, so carry a spare in an inside pocket.",
	"Study plan for the statistics exam. Week one covers probability distributions and the central limit theorem. Week two is hypothesis testing with t-tests and confidence intervals. Practise on the old exam papers from the library, and write down every formula you have to look up.",
	"Kitchen renovation quote. The contractor will remove the old cabinets, install the new counters in oak, and replace the sink with a double basin. The electrical work for the induction hob is included. Start date depends on the delivery of the tiles, expected in three weeks.",
}
