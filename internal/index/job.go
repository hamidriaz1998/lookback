package index

import (
	"sort"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// job is the work of one generation: a FIFO queue of file IDs, the file_index rows it has, and its counters.
// The active job and an optional rebuild job each have one; the worker owns the session they embed through.
type job struct {
	gen    int64
	space  contract.VectorSpace
	sess   contract.Session
	queue  []int64
	queued map[int64]bool
	rows   map[int64]contract.FileIndex
	begun  time.Time // when the job started; the ETA's estimating period counts from here
	items  int       // files committed by this job
	tokens float64   // tokens embedded by this job
	curr   string    // the file being processed
}

func newJob(gen int64, space contract.VectorSpace, sess contract.Session, rows []contract.FileIndex) *job {
	j := &job{gen: gen, space: space, sess: sess, queued: map[int64]bool{}, rows: map[int64]contract.FileIndex{}, begun: time.Now()}
	for _, r := range rows {
		j.rows[r.FileID] = r
	}
	return j
}

func (j *job) push(id int64) {
	if j.queued[id] {
		return
	}
	j.queued[id] = true
	j.queue = append(j.queue, id)
}

func (j *job) pop() (int64, bool) {
	if len(j.queue) == 0 {
		return 0, false
	}
	id := j.queue[0]
	j.queue = j.queue[1:]
	delete(j.queued, id)
	return id, true
}

// snapshotLocked builds the progress of j from memory only. The caller holds x.mu.
func (x *Indexer) snapshotLocked(j *job) contract.Progress {
	elapsed := time.Since(j.begun).Seconds()
	p := contract.Progress{Gen: j.gen, FilesTotal: len(x.files), Current: j.curr, Paused: x.pauseReason()}
	folderTotal := map[int64]int{}
	folderDone := map[int64]int{}
	for _, f := range x.files {
		folderTotal[f.FolderID]++
	}
	for id, row := range j.rows {
		f, ok := x.files[id]
		if !ok {
			continue
		}
		p.FilesDone++
		folderDone[f.FolderID]++
		if row.Status == contract.StatusError {
			p.Errors++
		}
	}
	ids := make([]int64, 0, len(x.folders))
	for id := range x.folders {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	for _, id := range ids {
		fo := x.folders[id].f
		p.PerFolder = append(p.PerFolder, contract.FolderProgress{FolderID: id, Path: fo.Path,
			FilesDone: folderDone[id], FilesTotal: folderTotal[id]})
	}
	if elapsed > 0 {
		p.FilesPerSec = float64(j.items) / elapsed
		p.TokensPerSec = j.tokens / elapsed
	}
	// Estimating until 30 s or 10 files have passed (SPEC section 11).
	if elapsed < estimatingFor.Seconds() && j.items < estimatingItems {
		p.ETA = -1
		return p
	}
	var textTok, imgTok float64
	for _, id := range j.queue {
		if f, ok := x.files[id]; ok {
			if f.Kind == contract.KindImage {
				imgTok += estimateTokens(f)
			} else {
				textTok += estimateTokens(f)
			}
		}
	}
	eta, ok := etaFor(textTok, imgTok, x.textRate, x.imageRate)
	if !ok {
		eta = -1
	}
	p.ETA = eta
	return p
}

const (
	estimatingFor   = 30 * time.Second
	estimatingItems = 10
)

// pauseReason returns the first reason set, in SPEC order, or PauseNone. The caller holds x.mu.
func (x *Indexer) pauseReason() contract.PauseReason {
	for _, r := range []contract.PauseReason{contract.PauseUser, contract.PauseBattery, contract.PauseSuspend, contract.PauseLab} {
		if x.reasons[r] {
			return r
		}
	}
	return contract.PauseNone
}
