package fake

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Store is an in-memory contract.Store. NewStore seeds it with the UI fixture: 300 indexed
// screenshots, a failed PDF, 101 files still pending, one active generation, and two installed
// combinations with test results. Every write checks ctx before and after taking the lock, and
// changes nothing when ctx is done.
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
	added := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	s := &Store{
		settings: map[string]string{"battery_pause": "on", "max_image_edge": "1600", "hotkey": "Super+Shift+Space"},
		folders: map[int64]contract.Folder{
			1: {ID: 1, Path: picturesFolder, AddedAt: added},
			2: {ID: 2, Path: documentFolder, Ignore: "*.tmp\n*.bak", AddedAt: added},
		},
		files:      map[int64]contract.FileRec{},
		index:      map[[2]int64]contract.FileIndex{},
		gens:       map[int64]contract.Generation{},
		combos:     map[string]contract.Installed{},
		items:      map[int64]contract.Item{},
		nextFolder: 3,
		nextGen:    2,
	}
	s.gens[1] = contract.Generation{
		ID: 1, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0,
		ModelRev: modelRev, State: contract.GenActive, CreatedAt: added,
	}
	for _, f := range indexedFiles() {
		s.files[f.ID] = f
		ix, items := simulate(f, 1)
		s.commitLocked(ix, items)
	}
	s.nextFile = imageCount + indexedDocuments + 1
	for _, f := range pendingFiles() {
		s.files[f.ID] = f
		s.nextFile = f.ID + 1
	}
	for _, in := range labInstalled() {
		s.combos[in.Combo.ID()] = in
	}
	return s
}

// lock takes the lock for a write. It returns the context error, before or after taking the
// lock, and then holds nothing.
func (s *Store) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return err
	}
	return nil
}

// GetSetting returns the stored value of key.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.settings[key]
	return v, ok, nil
}

// SetSetting stores a value for key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	s.settings[key] = value
	return nil
}

// SaveFolder inserts or updates f.
func (s *Store) SaveFolder(ctx context.Context, f *contract.Folder) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	if err := s.lock(ctx); err != nil {
		return err
	}
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
	if err := s.lock(ctx); err != nil {
		return err
	}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.folderFilesLocked(folderID), nil
}

// DeleteFile removes a file and its rows in every generation.
func (s *Store) DeleteFile(ctx context.Context, id int64) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[contract.Kind]int{}
	for _, f := range s.folderFilesLocked(folderID) {
		out[f.Kind]++
	}
	return out, nil
}

// ListFileIndex returns the file_index rows of one generation, by file ID.
func (s *Store) ListFileIndex(ctx context.Context, gen int64) ([]contract.FileIndex, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

// ClearFileIndex removes a file's row and items in one generation.
func (s *Store) ClearFileIndex(ctx context.Context, fileID, gen int64) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if _, ok := s.files[fileID]; !ok {
		return contract.ErrUnknownFile
	}
	delete(s.index, [2]int64{fileID, gen})
	for iid, it := range s.items {
		if it.FileID == fileID && it.Gen == gen {
			delete(s.items, iid)
		}
	}
	return nil
}

// CommitFile replaces the items of one file in one generation, upserts its row, and assigns item IDs.
// Nothing changes when it returns an error.
func (s *Store) CommitFile(ctx context.Context, idx contract.FileIndex, items []contract.Item) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if _, ok := s.files[idx.FileID]; !ok {
		return contract.ErrUnknownFile
	}
	if _, ok := s.gens[idx.Gen]; !ok {
		return contract.ErrUnknownGen
	}
	for _, it := range items {
		if it.Gen != idx.Gen || it.FileID != idx.FileID {
			return errors.New("fake store: item does not belong to the file and generation")
		}
	}
	s.commitLocked(idx, items)
	return nil
}

// commitLocked stores a row and its items, allocating new item IDs. The caller holds s.mu.
func (s *Store) commitLocked(idx contract.FileIndex, items []contract.Item) {
	for iid, it := range s.items {
		if it.FileID == idx.FileID && it.Gen == idx.Gen {
			delete(s.items, iid)
		}
	}
	for _, it := range items {
		it.ID = s.nextItem
		s.nextItem++
		it.Vector = append([]float32(nil), it.Vector...) // the store owns its copy (contract.Store)
		s.items[it.ID] = it
	}
	s.index[[2]int64{idx.FileID, idx.Gen}] = idx
}

// NewGeneration inserts g and sets its ID.
func (s *Store) NewGeneration(ctx context.Context, g *contract.Generation) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	g.ID = s.nextGen
	s.nextGen++
	g.State = contract.GenBuilding
	s.gens[g.ID] = *g
	return nil
}

// ListGenerations returns every generation, by ID.
func (s *Store) ListGenerations(ctx context.Context) ([]contract.Generation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contract.Generation, 0, len(s.gens))
	for _, g := range s.gens {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ActivateGeneration makes gen active and retires the previous active generation, in one step under the lock.
func (s *Store) ActivateGeneration(ctx context.Context, gen int64) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	g, ok := s.gens[gen]
	if !ok {
		return contract.ErrUnknownGen
	}
	if g.State == contract.GenRetired {
		return errors.New("fake store: a retired generation cannot be activated")
	}
	for id, other := range s.gens {
		if other.State == contract.GenActive && id != gen {
			other.State = contract.GenRetired
			s.gens[id] = other
		}
	}
	g.State = contract.GenActive
	s.gens[gen] = g
	return nil
}

// DeleteItems removes the items and rows of one generation. It refuses the active generation.
func (s *Store) DeleteItems(ctx context.Context, gen int64) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if g, ok := s.gens[gen]; ok && g.State == contract.GenActive {
		return errors.New("fake store: refusing to delete the active generation")
	}
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

// SaveCombo inserts or replaces an installed combination. It keeps its own copy of the benchmark.
func (s *Store) SaveCombo(ctx context.Context, in contract.Installed) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	s.combos[in.Combo.ID()] = cloneInstalled(in)
	return nil
}

// cloneInstalled returns a copy of in that shares no memory with it: the benchmark and its quality.
func cloneInstalled(in contract.Installed) contract.Installed {
	if in.Bench != nil {
		b := *in.Bench
		if b.Quality != nil {
			q := *b.Quality
			b.Quality = &q
		}
		in.Bench = &b
	}
	return in
}

// ListCombos returns every installed combination, by ID.
func (s *Store) ListCombos(ctx context.Context) ([]contract.Installed, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contract.Installed, 0, len(s.combos))
	for _, c := range s.combos {
		out = append(out, cloneInstalled(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Combo.ID() < out[j].Combo.ID() })
	return out, nil
}

// DeleteCombo removes an installed combination.
func (s *Store) DeleteCombo(ctx context.Context, c contract.Combo) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	delete(s.combos, c.ID())
	return nil
}

// RecentItems returns the most recently modified indexed files of gen, filtered before the limit.
func (s *Store) RecentItems(ctx context.Context, gen int64, limit int) ([]contract.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	type cand struct {
		f contract.FileRec
		h contract.Hit
	}
	var cands []cand
	for k, ix := range s.index {
		if k[1] != gen || ix.Status != contract.StatusDone {
			continue
		}
		f := s.files[ix.FileID]
		first, ok := s.firstItemLocked(gen, f.ID)
		if !ok {
			continue
		}
		cands = append(cands, cand{f, hitOf(f, first, 0)})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].f.MTime != cands[j].f.MTime {
			return cands[i].f.MTime > cands[j].f.MTime
		}
		return cands[i].f.ID < cands[j].f.ID
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]contract.Hit, len(cands))
	for i, c := range cands {
		out[i] = c.h
	}
	return out, nil
}

// firstItemLocked returns the item of file in gen with the lowest ID. The caller holds s.mu.
func (s *Store) firstItemLocked(gen, file int64) (contract.Item, bool) {
	var best contract.Item
	found := false
	for _, it := range s.items {
		if it.Gen == gen && it.FileID == file && (!found || it.ID < best.ID) {
			best, found = it, true
		}
	}
	return best, found
}

// hitOf builds a hit for item it of file f.
func hitOf(f contract.FileRec, it contract.Item, score float64) contract.Hit {
	return contract.Hit{
		ItemID: it.ID, FileID: f.ID, Path: f.Path, Kind: f.Kind, Modality: it.Modality,
		Unit: it.Unit, Snippet: it.Snippet, Score: score,
	}
}

// Search ranks the items of gen by cosine similarity to query.
func (s *Store) Search(ctx context.Context, gen int64, query []float32, kinds []contract.Kind, limit int) ([]contract.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rankLocked(gen, query, 0, kinds, limit), nil
}

// Similar ranks the items of gen against the stored vector of itemID, excluding that item's file.
// The seed must be an item of gen.
func (s *Store) Similar(ctx context.Context, gen int64, itemID int64, kinds []contract.Kind, limit int) ([]contract.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.gens[gen]; !ok {
		return nil, contract.ErrUnknownGen
	}
	src, ok := s.items[itemID]
	if !ok || src.Gen != gen {
		return nil, contract.ErrStaleItem
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
		best[it.FileID] = hitOf(f, it, sim)
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

// dot returns the dot product of two vectors, which is the cosine for unit vectors.
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

// folderFilesLocked returns the files of a folder, by ID. The caller holds s.mu.
func (s *Store) folderFilesLocked(folderID int64) []contract.FileRec {
	var out []contract.FileRec
	for _, f := range s.files {
		if f.FolderID == folderID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// activeGenID returns the active generation, or 0 when there is none.
func (s *Store) activeGenID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, g := range s.gens {
		if g.State == contract.GenActive {
			return id
		}
	}
	return 0
}

// progressCounts returns the job totals for gen: rows, errors, files, and the counts per folder.
func (s *Store) progressCounts(gen int64) (done, errs, total int, per []contract.FolderProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, ix := range s.index {
		if k[1] != gen {
			continue
		}
		done++
		if ix.Status == contract.StatusError {
			errs++
		}
	}
	for _, id := range s.folderIDsLocked() {
		fp := contract.FolderProgress{FolderID: id, Path: s.folders[id].Path}
		for _, f := range s.folderFilesLocked(id) {
			fp.FilesTotal++
			total++
			if _, ok := s.index[[2]int64{f.ID, gen}]; ok {
				fp.FilesDone++
			}
		}
		per = append(per, fp)
	}
	return done, errs, total, per
}

// folderIDsLocked returns the folder IDs in order. The caller holds s.mu.
func (s *Store) folderIDsLocked() []int64 {
	ids := make([]int64, 0, len(s.folders))
	for id := range s.folders {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// needingWork returns the files that need indexing for gen (contract.NeedsWork), by ID.
func (s *Store) needingWork(gen int64) []contract.FileRec {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contract.FileRec
	for _, f := range s.files {
		ix, ok := s.index[[2]int64{f.ID, gen}]
		var p *contract.FileIndex
		if ok {
			p = &ix
		}
		if contract.NeedsWork(f, p) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// doneFiles returns the files with a done row in gen, by ID.
func (s *Store) doneFiles(gen int64) []contract.FileRec {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contract.FileRec
	for k, ix := range s.index {
		if k[1] == gen && ix.Status == contract.StatusDone {
			out = append(out, s.files[k[0]])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// fileIndexRow returns the row of a file in gen.
func (s *Store) fileIndexRow(file, gen int64) (contract.FileIndex, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix, ok := s.index[[2]int64{file, gen}]
	return ix, ok
}

// fileByID returns a file.
func (s *Store) fileByID(id int64) (contract.FileRec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[id]
	return f, ok
}

// folderKnown reports whether a folder exists.
func (s *Store) folderKnown(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.folders[id]
	return ok
}

// errRows returns the file IDs with an error row in gen, by ID.
func (s *Store) errRows(gen int64) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int64
	for k, ix := range s.index {
		if k[1] == gen && ix.Status == contract.StatusError {
			out = append(out, k[0])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
