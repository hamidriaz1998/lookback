package fake

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Dataset sizes and paths shared by the fakes. The numbers match the UI guide's screens.
const (
	imageCount     = 300
	picturesFolder = "/home/demo/Pictures"
	documentFolder = "/home/demo/Documents"
	brokenPDF      = "/home/demo/Documents/report-old.pdf"
	dim            = 768
)

// Pending work: files the scan found and the indexer has not reached yet. At about 2,100 tokens
// each, the 100 files hold about 210,000 tokens, which is 37 minutes at 96 tokens per second.
const (
	pendingPictures  = 60
	pendingDocuments = 40
	tokensPerFile    = 2100.0
	indexedDocuments = 1 // the broken PDF, which is indexed with an error
)

// pendingFiles returns the files still to index, in queue order: screenshots first, then notes.
func pendingFiles() []contract.FileRec {
	out := make([]contract.FileRec, 0, pendingPictures+pendingDocuments)
	id := int64(imageCount + 2) // the broken PDF has ID imageCount+1
	for i := 0; i < pendingPictures; i++ {
		out = append(out, contract.FileRec{
			ID: id, FolderID: 1, Path: picturesFolder + "/" + screenshotName(imageCount+i), Kind: contract.KindImage,
			Size: 200_000 + int64(i*4241)%700_000, MTime: screenshotMTime(imageCount + i),
		})
		id++
	}
	for i := 0; i < pendingDocuments; i++ {
		out = append(out, contract.FileRec{
			ID: id, FolderID: 2, Path: fmt.Sprintf("%s/meeting-notes-%02d.md", documentFolder, i+1), Kind: contract.KindText,
			Size: 2_000 + int64(i*613)%30_000, MTime: screenshotMTime(imageCount + pendingPictures + i),
		})
		id++
	}
	return out
}

// screenshotName returns the name of the i-th screenshot, in the style of the desktop's screenshot tool.
func screenshotName(i int) string {
	t := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC).Add(time.Duration(i) * 47 * time.Minute)
	return fmt.Sprintf("screenshot-%s.png", t.Format("2006-01-02_15-04-05"))
}

// screenshotMTime returns the modification time of the i-th screenshot as Unix seconds.
func screenshotMTime(i int) int64 {
	return time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC).Add(time.Duration(i) * 47 * time.Minute).Unix()
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
