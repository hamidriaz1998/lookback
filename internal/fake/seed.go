package fake

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"path/filepath"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Fixture constants. The numbers match the UI guide's screens and the reference laptop.
const (
	imageCount       = 300 // indexed screenshots in ~/Pictures
	pendingPictures  = 60  // screenshots the scan found and the indexer has not reached yet
	pendingNotes     = 40  // text notes still to index
	indexedDocuments = 1   // the broken PDF, indexed with an error
	dim              = 768
	tokensPerFile    = 2100.0 // about the cost of one screenshot or note, for the ETA

	picturesFolder = "/home/demo/Pictures"
	documentFolder = "/home/demo/Documents"
	brokenPDF      = "/home/demo/Documents/report-old.pdf"
	invoicePDF     = "/home/demo/Documents/hosting-invoice-2026-08.pdf"
	brokenPDFError = "Encrypted PDF: no text and the pages can't be rendered"

	// modelRev is the pinned revision of ggml-org/embeddinggemma-2-GGUF (SPEC section 4).
	modelRev = "58faf90940886df2d62be555c8765d00dac362c5"
)

// screenshotName returns the name of the i-th screenshot, in the style of the desktop's screenshot tool.
func screenshotName(i int) string {
	return fmt.Sprintf("screenshot-%s.png", screenshotTime(i).Format("2006-01-02_15-04-05"))
}

// screenshotTime returns the capture time of the i-th screenshot.
func screenshotTime(i int) time.Time {
	return time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC).Add(time.Duration(i) * 47 * time.Minute)
}

// screenshotMTime returns the modification time of the i-th screenshot in Unix nanoseconds.
func screenshotMTime(i int) int64 { return screenshotTime(i).UnixNano() }

// indexedFiles returns the files that are indexed in the first generation: the screenshots and the broken PDF.
func indexedFiles() []contract.FileRec {
	out := make([]contract.FileRec, 0, imageCount+indexedDocuments)
	for i := 0; i < imageCount; i++ {
		out = append(out, contract.FileRec{
			ID: int64(i + 1), FolderID: 1, Path: picturesFolder + "/" + screenshotName(i), Kind: contract.KindImage,
			Size: 120_000 + int64(i*7919)%900_000, MTime: screenshotMTime(i),
		})
	}
	out = append(out, contract.FileRec{
		ID: imageCount + 1, FolderID: 2, Path: brokenPDF, Kind: contract.KindPDF,
		Size: 4_200_000, MTime: screenshotMTime(0),
	})
	return out
}

// pendingFiles returns the files the scan found and the indexer has not reached, in queue order:
// screenshots, then notes, then one PDF with a text page and a scanned page.
func pendingFiles() []contract.FileRec {
	out := make([]contract.FileRec, 0, pendingPictures+pendingNotes+1)
	id := int64(imageCount + indexedDocuments + 1)
	for i := 0; i < pendingPictures; i++ {
		out = append(out, contract.FileRec{
			ID: id, FolderID: 1, Path: picturesFolder + "/" + screenshotName(imageCount+i), Kind: contract.KindImage,
			Size: 200_000 + int64(i*4241)%700_000, MTime: screenshotMTime(imageCount + i),
		})
		id++
	}
	for i := 0; i < pendingNotes; i++ {
		out = append(out, contract.FileRec{
			ID: id, FolderID: 2, Path: fmt.Sprintf("%s/meeting-notes-%02d.md", documentFolder, i+1), Kind: contract.KindText,
			Size: 2_000 + int64(i*613)%30_000, MTime: screenshotMTime(imageCount + pendingPictures + i),
		})
		id++
	}
	out = append(out, contract.FileRec{
		ID: id, FolderID: 2, Path: invoicePDF, Kind: contract.KindPDF,
		Size: 310_000, MTime: screenshotMTime(imageCount + pendingPictures + pendingNotes),
	})
	return out
}

// simulate returns what indexing f produces for generation gen: its file_index row and its items.
// Item vectors depend on the generation, because each generation is its own vector space.
func simulate(f contract.FileRec, gen int64) (contract.FileIndex, []contract.Item) {
	ix := contract.FileIndex{
		FileID: f.ID, Gen: gen, Size: f.Size, MTime: f.MTime,
		Status: contract.StatusDone, Tokens: tokensPerFile, Ms: 2400,
	}
	item := func(unit int, modality string, start, end int, snippet string) contract.Item {
		return contract.Item{
			Gen: gen, FileID: f.ID, Unit: unit, Start: start, End: end, Modality: modality, Snippet: snippet,
			Vector: unitVector(hashSeed(fmt.Sprint(f.ID, unit, gen))),
		}
	}
	switch {
	case f.Path == brokenPDF:
		ix.Status = contract.StatusError
		ix.Error = brokenPDFError
		return ix, nil
	case f.Kind == contract.KindImage:
		return ix, []contract.Item{item(0, "image", -1, -1, "")}
	case f.Path == invoicePDF:
		text := "Invoice for the hosting renewal, due 15 September 2026."
		return ix, []contract.Item{item(1, "text", 0, len(text), text), item(2, "image", -1, -1, "")}
	default:
		text := "Notes from " + filepath.Base(f.Path) + "."
		return ix, []contract.Item{item(0, "text", 0, len(text), text)}
	}
}

// unitVector returns a deterministic L2-normalized vector of dim values for seed.
func unitVector(seed uint64) []float32 {
	r := rand.New(rand.NewSource(int64(seed)))
	v := make([]float32, dim)
	var n float64
	for i := range v {
		x := r.NormFloat64()
		v[i] = float32(x)
		n += x * x
	}
	s := float32(math.Sqrt(n))
	for i := range v {
		v[i] /= s
	}
	return v
}

// hashSeed hashes s with FNV-1a, for seeding vectors from text or bytes.
func hashSeed(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}
