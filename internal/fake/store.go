package fake

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Store is an in-memory contract.Store. NewStore seeds it with the UI guide's data:
// 300 indexed screenshots, a document folder with one failed PDF, one active generation,
// and two installed combinations with test results.
type Store struct {
	mu       sync.Mutex
	settings map[string]string
	folders  map[int64]contract.Folder
	files    map[int64]contract.FileRec
	index    map[[2]int64]contract.FileIndex // key: file ID, generation
	gens     map[int64]contract.Generation
	combos   map[string]contract.Installed
	items    map[int64]contract.Item

	nextFolder, nextFile, nextGen, nextItem int64
}

// NewStore returns a Store seeded with the UI fixture data.
func NewStore() *Store {
	s := &Store{
		settings: map[string]string{"battery_pause": "on", "max_image_edge": "1600", "hotkey": "Super+Shift+Space"},
		folders:  map[int64]contract.Folder{},
		files:    map[int64]contract.FileRec{},
		index:    map[[2]int64]contract.FileIndex{},
		gens:     map[int64]contract.Generation{},
		combos:   map[string]contract.Installed{},
		items:    map[int64]contract.Item{},
	}
	added := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	s.folders[1] = contract.Folder{ID: 1, Path: picturesFolder, AddedAt: added}
	s.folders[2] = contract.Folder{ID: 2, Path: documentFolder, Ignore: "*.tmp\n*.bak", AddedAt: added}
	s.nextFolder = 3

	s.gens[1] = contract.Generation{
		ID: 1, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0,
		ModelRev: "58faf90940886df2d62be555c8765d00dac362c5", State: contract.GenActive, CreatedAt: added,
	}
	s.nextGen = 2

	for i := 0; i < imageCount; i++ {
		id := int64(i + 1)
		s.files[id] = contract.FileRec{
			ID: id, FolderID: 1, Path: picturesFolder + "/" + screenshotName(i), Kind: contract.KindImage,
			Size: 120_000 + int64(i*7919)%900_000, MTime: screenshotMTime(i),
		}
		s.index[[2]int64{id, 1}] = contract.FileIndex{
			FileID: id, Gen: 1, Size: s.files[id].Size, MTime: s.files[id].MTime,
			Status: contract.StatusDone, Tokens: 1200 + int64(i*37)%1400, Ms: 2400 + int64(i*53)%900,
		}
		s.items[id] = contract.Item{
			ID: id, Gen: 1, FileID: id, Unit: 0, Start: -1, End: -1, Modality: "image",
			Vector: unitVector(uint64(id) * 2654435761),
		}
	}
	s.nextFile = imageCount + 1
	s.nextItem = imageCount + 1

	pdf := contract.FileRec{ID: s.nextFile, FolderID: 2, Path: brokenPDF, Kind: contract.KindPDF, Size: 4_200_000, MTime: screenshotMTime(0)}
	s.files[pdf.ID] = pdf
	s.index[[2]int64{pdf.ID, 1}] = contract.FileIndex{
		FileID: pdf.ID, Gen: 1, Size: pdf.Size, MTime: pdf.MTime, Status: contract.StatusError,
		Error: "Encrypted PDF: no text and the pages can't be rendered",
	}
	s.nextFile++

	// Pending files: found by the scan, not yet indexed. The indexer works through them.
	for _, f := range pendingFiles() {
		s.files[f.ID] = f
		s.nextFile = f.ID + 1
	}

	s.combos[contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}.ID()] = contract.Installed{
		Combo:       contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0},
		InstalledAt: added.Add(2 * time.Hour),
		Bench:       &contract.BenchResult{ImageMS: 2410, TextTokensPerSec: 312, PeakRAM: 900 << 20, VRAM: 1200 << 20, Quality: fp(0.996)},
	}
	s.combos[contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}.ID()] = contract.Installed{
		Combo:       contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16},
		InstalledAt: added.Add(3 * time.Hour),
		Bench:       &contract.BenchResult{ImageMS: 2683, TextTokensPerSec: 298, PeakRAM: 1100 << 20, VRAM: 1600 << 20, Quality: fp(1.0)},
	}
	return s
}

// fp returns a pointer to v.
func fp(v float64) *float64 { return &v }

// GetSetting returns the stored value of key.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.settings[key]
	return v, ok, nil
}

// SetSetting stores a value for key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
	return nil
}

// SaveFolder inserts or updates f.
func (s *Store) SaveFolder(ctx context.Context, f *contract.Folder) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.ID == 0 {
		f.ID = s.nextFolder
		s.nextFolder++
		f.AddedAt = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	}
	s.folders[f.ID] = *f
	return nil
}

// ListFolders returns every folder, by ID.
func (s *Store) ListFolders(ctx context.Context) ([]contract.Folder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contract.Folder, 0, len(s.folders))
	for _, f := range s.folders {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// DeleteFolder removes a folder, its files and their rows.
func (s *Store) DeleteFolder(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for fid, f := range s.files {
		if f.FolderID == id {
			s.deleteFileLocked(fid)
		}
	}
	delete(s.folders, id)
	return nil
}

// SaveFile inserts or updates f, keyed by path.
func (s *Store) SaveFile(ctx context.Context, f *contract.FileRec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.ID == 0 {
		for _, old := range s.files {
			if old.Path == f.Path {
				f.ID = old.ID
			}
		}
	}
	if f.ID == 0 {
		f.ID = s.nextFile
		s.nextFile++
	}
	s.files[f.ID] = *f
	return nil
}

// ListFiles returns the files of one folder, by ID.
func (s *Store) ListFiles(ctx context.Context, folderID int64) ([]contract.FileRec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contract.FileRec
	for _, f := range s.files {
		if f.FolderID == folderID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// DeleteFile removes a file and its rows in every generation.
func (s *Store) DeleteFile(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteFileLocked(id)
	return nil
}

// deleteFileLocked removes a file's rows. The caller holds s.mu.
func (s *Store) deleteFileLocked(id int64) {
	delete(s.files, id)
	for k := range s.index {
		if k[0] == id {
			delete(s.index, k)
		}
	}
	for iid, it := range s.items {
		if it.FileID == id {
			delete(s.items, iid)
		}
	}
}

// CountFiles counts the files of each kind in one folder.
func (s *Store) CountFiles(ctx context.Context, folderID int64) (map[contract.Kind]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[contract.Kind]int{}
	for _, f := range s.files {
		if f.FolderID == folderID {
			out[f.Kind]++
		}
	}
	return out, nil
}

// RecentFiles returns the limit most recently modified files.
func (s *Store) RecentFiles(ctx context.Context, limit int) ([]contract.FileRec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contract.FileRec, 0, len(s.files))
	for _, f := range s.files {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MTime != out[j].MTime {
			return out[i].MTime > out[j].MTime
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListFileIndex returns the file_index rows of one generation, by file ID.
func (s *Store) ListFileIndex(ctx context.Context, gen int64) ([]contract.FileIndex, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contract.FileIndex
	for k, v := range s.index {
		if k[1] == gen {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FileID < out[j].FileID })
	return out, nil
}

// CommitFile replaces the items of one file in one generation and upserts its file_index row.
func (s *Store) CommitFile(ctx context.Context, idx contract.FileIndex, items []contract.Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[idx.FileID]; !ok {
		return errors.New("fake store: unknown file")
	}
	for _, it := range items {
		if it.Gen != idx.Gen || it.FileID != idx.FileID {
			return errors.New("fake store: item does not belong to the file and generation")
		}
	}
	for iid, it := range s.items {
		if it.FileID == idx.FileID && it.Gen == idx.Gen {
			delete(s.items, iid)
		}
	}
	for _, it := range items {
		if it.ID == 0 {
			it.ID = s.nextItem
			s.nextItem++
		}
		s.items[it.ID] = it
	}
	s.index[[2]int64{idx.FileID, idx.Gen}] = idx
	return nil
}

// NewGeneration inserts g.
func (s *Store) NewGeneration(ctx context.Context, g *contract.Generation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.ID = s.nextGen
	s.nextGen++
	s.gens[g.ID] = *g
	return nil
}

// ListGenerations returns every generation, by ID.
func (s *Store) ListGenerations(ctx context.Context) ([]contract.Generation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contract.Generation, 0, len(s.gens))
	for _, g := range s.gens {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SetGenerationState changes the state of one generation.
func (s *Store) SetGenerationState(ctx context.Context, id int64, state contract.GenState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.gens[id]
	if !ok {
		return errors.New("fake store: unknown generation")
	}
	g.State = state
	s.gens[id] = g
	return nil
}

// DeleteItems removes the items and file_index rows of one generation.
func (s *Store) DeleteItems(ctx context.Context, gen int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for iid, it := range s.items {
		if it.Gen == gen {
			delete(s.items, iid)
		}
	}
	for k := range s.index {
		if k[1] == gen {
			delete(s.index, k)
		}
	}
	return nil
}

// SaveCombo inserts or replaces an installed combination.
func (s *Store) SaveCombo(ctx context.Context, in contract.Installed) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.combos[in.Combo.ID()] = in
	return nil
}

// ListCombos returns every installed combination, by ID.
func (s *Store) ListCombos(ctx context.Context) ([]contract.Installed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contract.Installed, 0, len(s.combos))
	for _, c := range s.combos {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Combo.ID() < out[j].Combo.ID() })
	return out, nil
}

// DeleteCombo removes an installed combination.
func (s *Store) DeleteCombo(ctx context.Context, c contract.Combo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.combos, c.ID())
	return nil
}

// Search ranks the items of gen by cosine similarity to query. Score is the similarity (1 minus the
// cosine distance); the search package adds the filename boost.
func (s *Store) Search(ctx context.Context, gen int64, query []float32, kinds []contract.Kind, limit int) ([]contract.Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rankLocked(gen, query, 0, kinds, limit), nil
}

// Similar ranks the items of gen against the stored vector of itemID, excluding that item's file.
func (s *Store) Similar(ctx context.Context, gen int64, itemID int64, kinds []contract.Kind, limit int) ([]contract.Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.items[itemID]
	if !ok {
		return nil, errors.New("fake store: unknown item")
	}
	return s.rankLocked(gen, src.Vector, src.FileID, kinds, limit), nil
}

// rankLocked returns the best hit of each file in gen, by similarity, skipping excludeFile
// (0 skips none). The caller holds s.mu.
func (s *Store) rankLocked(gen int64, query []float32, excludeFile int64, kinds []contract.Kind, limit int) []contract.Hit {
	allowed := func(k contract.Kind) bool {
		if len(kinds) == 0 {
			return true
		}
		for _, want := range kinds {
			if want == k {
				return true
			}
		}
		return false
	}
	best := map[int64]contract.Hit{}
	for _, it := range s.items {
		if it.Gen != gen || it.FileID == excludeFile {
			continue
		}
		f, ok := s.files[it.FileID]
		if !ok || !allowed(f.Kind) {
			continue
		}
		sim := dot(query, it.Vector)
		if cur, seen := best[it.FileID]; seen && cur.Score >= sim {
			continue
		}
		best[it.FileID] = contract.Hit{
			ItemID: it.ID, FileID: it.FileID, Path: f.Path, Kind: f.Kind,
			Modality: it.Modality, Unit: it.Unit, Snippet: it.Snippet, Score: sim,
		}
	}
	out := make([]contract.Hit, 0, len(best))
	for _, h := range best {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].FileID < out[j].FileID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// dot returns the dot product of two vectors of equal length, which is the cosine for unit vectors.
func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		if i < len(b) {
			sum += float64(a[i]) * float64(b[i])
		}
	}
	return sum
}

// Close does nothing for the in-memory store.
func (s *Store) Close() error { return nil }
