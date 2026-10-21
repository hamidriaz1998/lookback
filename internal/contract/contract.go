// Package contract holds the types and interfaces that Lookback packages share.
// Packages talk through these interfaces; each one has a fake in internal/fake.
// Changing an interface needs an approved change here that updates the fakes too.
package contract

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"
)

// Errors that implementations return. Callers compare them with errors.Is.
var (
	// ErrNotStarted is returned by Indexer commands before Start.
	ErrNotStarted = errors.New("indexer not started")
	// ErrUnknownFile is returned when a file ID is not in the store.
	ErrUnknownFile = errors.New("unknown file")
	// ErrUnknownFolder is returned when a folder ID is not in the store.
	ErrUnknownFolder = errors.New("unknown folder")
	// ErrUnknownGen is returned when a generation ID is not in the store.
	ErrUnknownGen = errors.New("unknown generation")
	// ErrStaleItem is returned by Store.Similar when the seed item is not in the requested generation
	// (or does not exist). The caller must drop the seed, since its vector is in another vector space.
	ErrStaleItem = errors.New("item is not in the requested generation")
	// ErrRebuildBusy is returned by Indexer.Rebuild while another rebuild runs.
	ErrRebuildBusy = errors.New("a rebuild is already running")
	// ErrUnknownCombo is returned by Catalog.Assets for a combination the catalog does not offer.
	ErrUnknownCombo = errors.New("combination not in the catalog")
	// ErrUnknownAsset is returned by Downloader for an asset it does not hold.
	ErrUnknownAsset = errors.New("unknown asset")
	// ErrConflict is returned by Downloader.Ensure when a key is held with a different descriptor.
	ErrConflict = errors.New("asset key is held with a different descriptor")
	// ErrNotReady is returned by a Session that is not ready to embed (its sidecar is stopped or failed).
	ErrNotReady = errors.New("runtime is not ready")
	// ErrRestarting is returned by a Session whose sidecar is starting or restarting. Callers retry after
	// Runtime.Use returns.
	ErrRestarting = errors.New("runtime is restarting")
	// ErrTooManySpaces is returned by Runtime.Session when two other vector spaces are already open.
	ErrTooManySpaces = errors.New("two vector spaces are already open")
	// ErrIncomplete is returned by Store.ActivateGeneration for a generation that does not cover every file.
	ErrIncomplete = errors.New("generation does not cover every file")
	// ErrStopped is returned by Indexer commands after the indexer's lifetime ends, and by Runtime waits
	// that are cut short by Stop.
	ErrStopped = errors.New("service is stopped")
)

// Backend is a llama.cpp compute backend.
type Backend string

// The compute backends that Lookback offers.
const (
	BackendCPU    Backend = "cpu"
	BackendVulkan Backend = "vulkan"
	BackendCUDA12 Backend = "cuda12"
	BackendCUDA13 Backend = "cuda13"
)

// Quant is the precision of one model part: the text model or the encoders.
type Quant string

// The precisions that Lookback offers. F16 is never offered (SPEC section 4).
const (
	QuantQ8_0 Quant = "q8_0"
	QuantBF16 Quant = "bf16"
)

// Combo is one combination: a backend with a precision for the text model and one for the encoders.
type Combo struct {
	Backend   Backend
	TextQuant Quant
	EncQuant  Quant
}

// ID returns the key of c in the combos table, such as "vulkan/bf16/q8_0".
func (c Combo) ID() string {
	return string(c.Backend) + "/" + string(c.TextQuant) + "/" + string(c.EncQuant)
}

// BenchResult is one row of the test lab (SPEC section 6, step 2) for a combination.
type BenchResult struct {
	ImageMS          float64  // median wall time per image request, in milliseconds
	TextTokensPerSec float64  // usage.prompt_tokens divided by wall time over the text chunks
	PeakRAM          int64    // bytes
	VRAM             int64    // bytes, approximate; 0 for the CPU backend
	Quality          *float64 // mean cosine against the BF16 reference; nil means n/a
	QualityReason    string   // why Quality is nil, for the tooltip, such as "needs an installed BF16 combination"
}

// Installed is a combination whose parts are on disk, with its latest test result if it has one.
type Installed struct {
	Combo       Combo
	InstalledAt time.Time
	Bench       *BenchResult // nil until the lab has tested it
}

// Kind is the type of a file, as SPEC section 9 groups them.
type Kind string

// The file kinds.
const (
	KindImage  Kind = "image"
	KindText   Kind = "text"
	KindPDF    Kind = "pdf"
	KindOffice Kind = "office"
)

// FileRec is a file that the index knows about.
type FileRec struct {
	ID, FolderID int64
	Path         string
	Kind         Kind
	Size         int64 // bytes
	MTime        int64 // modification time in Unix nanoseconds
}

// Unit is one thing to embed: a text chunk or an image.
type Unit struct {
	FileID     int64
	Unit       int    // page, slide or sheet; 0 for whole files
	Start, End int    // byte range in the text; -1 for images
	Text       string // chunk text without the prompt prefix (text units)
	Image      []byte // PNG or JPEG bytes (image units)
	Snippet    string
	EstTokens  int // for the ETA
}

// Item is a stored vector: one unit of one file, in one generation.
type Item struct {
	ID, Gen, FileID int64 // ID is assigned by the store when the item is committed; callers leave it 0
	Unit            int
	Start, End      int       // byte range in the text; -1 for images
	Modality        string    // "text" or "image"
	Snippet         string    // the first 300 characters, whitespace collapsed
	Vector          []float32 // 768 values, L2-normalized
}

// Hit is one result: the best item of a file.
type Hit struct {
	ItemID, FileID int64
	Path           string
	Kind           Kind
	Modality       string // "text" or "image"; a PDF page rendered as an image has Kind pdf and Modality image
	Unit           int
	Snippet        string
	Score          float64 // cosine similarity; the search package adds the filename boost
}

// FolderProgress is the indexing progress of one watched folder.
type FolderProgress struct {
	FolderID   int64
	Path       string
	FilesDone  int
	FilesTotal int
}

// PauseReason says why indexing is paused. PauseNone means it runs.
type PauseReason string

// The pause reasons.
const (
	PauseNone    PauseReason = ""
	PauseUser    PauseReason = "user"
	PauseBattery PauseReason = "battery"
	PauseSuspend PauseReason = "suspend"
	PauseLab     PauseReason = "lab" // a test lab run is using the runtime (SPEC section 8)
)

// Progress is a snapshot of one indexing job: the active generation, or the building one during a rebuild.
// Publishers send at most 4 snapshots per second per stream.
type Progress struct {
	Gen                       int64 // generation this snapshot describes
	FilesDone, FilesTotal     int   // files with a file_index row in Gen, and the files this job covers
	FilesPerSec, TokensPerSec float64
	ETA                       time.Duration // negative while estimating
	Current                   string        // the file being processed
	PerFolder                 []FolderProgress
	Errors                    int // file_index rows with status error in Gen
	Paused                    PauseReason
}

// Token is one token of a text, from Embedder.Tokenize. Start and End are the byte range of the
// token in the text, so text[Start:End] is its source. A token can cover part of a UTF-8 character:
// byte-fallback tokens do, and Piece then holds the raw bytes (SPEC section 9, docs/spikes.md spike 3).
type Token struct {
	ID         int32
	Piece      string
	Start, End int
}

// Embedder turns text and images into vectors. The sidecar implements it.
type Embedder interface {
	// EmbedTexts embeds prompts and returns the vectors and the prompt tokens used. Each prompt
	// already has its prefix and must be within the 512-token cap (SPEC section 9).
	EmbedTexts(ctx context.Context, prompts []string) (vecs [][]float32, tokens int, err error)
	// EmbedImage embeds one PNG or JPEG image.
	EmbedImage(ctx context.Context, img []byte, mime string) (vec []float32, tokens int, err error)
	// Tokenize splits text into tokens with their byte ranges, for chunk windows.
	Tokenize(ctx context.Context, text string) ([]Token, error)
}

// Status is the outcome of indexing one file for one generation.
type Status string

// The statuses of a file_index row.
const (
	StatusDone    Status = "done"
	StatusError   Status = "error"
	StatusSkipped Status = "skipped"
)

// GenState is the state of a generation.
type GenState string

// The generation states. At most one generation is active at a time.
const (
	GenBuilding GenState = "building"
	GenActive   GenState = "active"
	GenRetired  GenState = "retired"
)

// Folder is a watched folder. Ignore holds one exclusion pattern per line.
type Folder struct {
	ID      int64
	Path    string
	Ignore  string
	AddedAt time.Time
}

// FileIndex records what was indexed for one file in one generation.
type FileIndex struct {
	FileID, Gen int64
	Size        int64 // bytes, as the file was when indexed
	MTime       int64 // Unix nanoseconds, as the file was when indexed
	Status      Status
	Error       string // set when Status is StatusError
	Tokens, Ms  int64
}

// NeedsWork reports whether file f must be indexed for a generation (SPEC section 10): it has no
// file_index row there (ix is nil), or its size or mtime differs from the row's.
func NeedsWork(f FileRec, ix *FileIndex) bool {
	return ix == nil || ix.Size != f.Size || ix.MTime != f.MTime
}

// VectorSpace is the identity of a vector space: the precisions and the model revision (SPEC sections 10
// and 17). Vectors from different spaces are not comparable, so a generation has one space and a
// Session returns vectors of one space.
type VectorSpace struct {
	TextQuant, EncQuant Quant
	ModelRev            string
}

// Generation is one vector space's index: one set of items for every indexed file (SPEC section 10, Q26).
type Generation struct {
	ID        int64
	Space     VectorSpace
	State     GenState
	CreatedAt time.Time
}

// Store persists folders, files, items and settings (SPEC section 10). Reads of items are scoped to
// one generation. Every write method is atomic: on error (including a canceled context) nothing changes.
//
// Value ownership: the store copies what it keeps. CommitFile copies each item's vector, so the caller
// may reuse its buffer after the call returns. SaveCombo copies the benchmark and its quality, and
// ListCombos returns copies, so a caller that changes a returned value changes nothing in the store.
type Store interface {
	// GetSetting returns the value of key; ok is false when it is not set.
	GetSetting(ctx context.Context, key string) (value string, ok bool, err error)
	// SetSetting stores a value for key.
	SetSetting(ctx context.Context, key, value string) error

	// SaveFolder inserts f when f.ID is 0 and updates it otherwise. It sets f.ID.
	SaveFolder(ctx context.Context, f *Folder) error
	// ListFolders returns every folder, oldest first.
	ListFolders(ctx context.Context) ([]Folder, error)
	// DeleteFolder removes a folder and all its files and their rows.
	DeleteFolder(ctx context.Context, id int64) error

	// SaveFile inserts f when f.ID is 0 and updates it otherwise, keyed by path. It sets f.ID.
	SaveFile(ctx context.Context, f *FileRec) error
	// ListFiles returns the files of one folder.
	ListFiles(ctx context.Context, folderID int64) ([]FileRec, error)
	// DeleteFile removes a file and its file_index and items rows in every generation.
	DeleteFile(ctx context.Context, id int64) error
	// CountFiles returns the number of files of each kind in one folder.
	CountFiles(ctx context.Context, folderID int64) (map[Kind]int, error)

	// ListFileIndex returns the file_index rows of one generation.
	ListFileIndex(ctx context.Context, gen int64) ([]FileIndex, error)
	// ClearFileIndex removes a file's file_index row and items in one generation, so the file
	// needs work again even if it did not change. Retry uses it.
	ClearFileIndex(ctx context.Context, fileID, gen int64) error
	// CommitFile replaces the items of idx.FileID in idx.Gen and upserts its file_index row, in one
	// transaction. The store assigns every item ID and ignores Item.ID. Every item must have Gen and
	// FileID set to idx.Gen and idx.FileID; otherwise nothing changes and an error is returned.
	CommitFile(ctx context.Context, idx FileIndex, items []Item) error

	// NewGeneration inserts g in state building and sets g.ID.
	NewGeneration(ctx context.Context, g *Generation) error
	// ListGenerations returns every generation, oldest first.
	ListGenerations(ctx context.Context) ([]Generation, error)
	// GenerationComplete reports whether every tracked file has a file_index row in gen whose size and mtime
	// equal the file's current ones, with status done, error or skipped (SPEC section 10).
	GenerationComplete(ctx context.Context, gen int64) (bool, error)
	// ActivateGeneration makes gen the active generation and retires the previous active one, in one
	// transaction. It checks GenerationComplete in that transaction and returns ErrIncomplete when gen is not
	// complete, and then nothing changes. The retired generation's items stay until DeleteItems removes them.
	ActivateGeneration(ctx context.Context, gen int64) error
	// DeleteItems removes the items and file_index rows of one generation. It refuses the active one.
	DeleteItems(ctx context.Context, gen int64) error

	// SaveCombo inserts or replaces an installed combination, keyed by Combo.ID.
	SaveCombo(ctx context.Context, in Installed) error
	// ListCombos returns every installed combination.
	ListCombos(ctx context.Context) ([]Installed, error)
	// DeleteCombo removes an installed combination.
	DeleteCombo(ctx context.Context, c Combo) error

	// RecentItems returns the limit most recently modified files that are indexed (status done) in gen,
	// one hit each, with the item ID of its first item. Files are filtered before the limit applies.
	RecentItems(ctx context.Context, gen int64, limit int) ([]Hit, error)
	// Search returns the best item of each file in gen, nearest to query first, at most limit hits.
	// kinds, when not empty, limits the files to those kinds.
	Search(ctx context.Context, gen int64, query []float32, kinds []Kind, limit int) ([]Hit, error)
	// Similar returns the files nearest to the stored vector of itemID, excluding its own file. The seed
	// must be an item of gen; otherwise it returns ErrStaleItem.
	Similar(ctx context.Context, gen int64, itemID int64, kinds []Kind, limit int) ([]Hit, error)

	// Close releases the database.
	Close() error
}

// Extractor turns one file into units. One Extractor per kind; the registry is in internal/extract.
type Extractor interface {
	// Extract streams the units of one file to emit, in order. Images come back already resized and encoded.
	Extract(ctx context.Context, f FileRec, emit func(Unit) error) error
}

// Streams. Every Subscribe method returns its own channel. The channel starts with the current snapshot
// and then carries each later update, in the order the state changed. A reader that falls behind gets the
// newest value per key: an update it has not read is replaced by a newer one with the same key. A terminal
// value is never replaced or dropped; each stream names its terminal values. The channel closes when the
// subscriber's ctx ends or the service stops. Each service's own type documents its keys and terminal values.
//
// Indexer runs the indexing job: queue, pipeline, pauses, progress, and the user commands from
// Activity and Folders. Commands return ErrNotStarted before Start, and the context's error when ctx
// is already done. They queue work and return; the job does the work in the background.
//
// Lifetime: Start takes the job's lifetime context, and the job, its commits and its generation changes
// all use it. Start rejects a context that is already done. When the lifetime ends, the job stops and
// its streams close. After that every command returns ErrStopped and changes nothing, and Pause and Resume
// change nothing.
type Indexer interface {
	// Start begins the job in the background. Call it once; a second call is an error.
	Start(ctx context.Context) error
	// Pause stops the job for reason. Pauses for different reasons stack.
	Pause(reason PauseReason)
	// Resume lifts the pause for reason.
	Resume(reason PauseReason)
	// Retry queues the given files for indexing again in the active generation, even if they did not
	// change (SPEC section 11, Activity's Retry). ErrUnknownFile for an ID not in the store.
	Retry(ctx context.Context, fileIDs []int64) error
	// RetryAll queues every file with an error row in the active generation.
	RetryAll(ctx context.Context) error
	// Rescan queues the files of one folder that need work (NeedsWork), as the startup rescan does.
	Rescan(ctx context.Context, folderID int64) error
	// Rebuild starts a building generation for the precisions text and enc, and indexes every tracked file
	// into it, including files still pending in the active generation. The active generation keeps serving
	// until the building one is complete, and then the rebuild activates it (SPEC section 10). While the
	// rebuild runs, a file that changes is queued for both generations. Before activation the rebuild
	// queues every file whose row in the building generation is missing or stale, so no file is lost. It
	// returns the new generation's ID. ErrRebuildBusy while another rebuild runs.
	Rebuild(ctx context.Context, text, enc Quant) (gen int64, err error)
	// Subscribe streams snapshots of the active generation's job, keyed by Gen. No value is terminal.
	Subscribe(ctx context.Context) <-chan Progress
	// SubscribeRebuild streams snapshots of the building generation during a rebuild, keyed by Gen. It is
	// quiet when no rebuild runs. The last snapshot of a rebuild, with FilesDone equal to FilesTotal, is
	// terminal.
	SubscribeRebuild(ctx context.Context) <-chan Progress
}

// ThemeSource provides a theme. Built-in themes and the Omarchy source implement it.
type ThemeSource interface {
	// ID is the stable key of the source, stored in settings.
	ID() string
	// Name is the label shown in the theme picker.
	Name() string
	// Theme returns the theme built from a copy of base.
	Theme(base *ui.Theme) *ui.Theme
	// Changed sends when the theme changes. It is nil for fixed themes.
	Changed() <-chan struct{}
}

// OS names an operating system for the catalog: "linux" or "windows".
type OS string

// Part is the role of a file in a combination.
type Part string

// The parts of a combination.
const (
	PartRuntime Part = "runtime" // a llama.cpp archive for one backend
	PartText    Part = "text"    // the text model GGUF
	PartEncoder Part = "encoder" // the mmproj GGUF
)

// Asset is one pinned file the app may download (SPEC section 4).
type Asset struct {
	Name    string  // the file name in its release or repository; for display
	URL     string  // the pinned URL
	Bytes   int64   // expected size
	SHA256  string  // hex; empty for runtime archives until WP1 pins them
	Part    Part    // what the file is
	Backend Backend // runtime assets only
	Quant   Quant   // model assets only
	Archive bool    // .tar.gz or .zip: unpacked into runtimes/<tag>-<backend>/
}

// Key returns the identity of the asset, which Downloader uses for everything it tracks. It is the
// SHA-256. An unpinned asset, such as a runtime archive until WP1 pins it, has no SHA-256, so its key
// is its URL, which names the release tag. A new pin has a new SHA-256, so it is a new asset (SPEC section 17).
func (a Asset) Key() string {
	if a.SHA256 != "" {
		return a.SHA256
	}
	return a.URL
}

// Catalog lists the pinned assets. It is compiled into the binary (SPEC section 4).
type Catalog interface {
	// Assets returns every file that combination c needs on os: its runtime archives and its model files.
	// It returns ErrUnknownCombo for a combination the catalog does not offer.
	Assets(os OS, c Combo) ([]Asset, error)
	// Backends returns the backends the catalog offers on os. Detection decides which are candidates.
	Backends(os OS) []Backend
}

// FileState is the state of one download.
type FileState string

// The download states, as the lab and Models screens show them.
const (
	FileQueued      FileState = "queued"
	FileDownloading FileState = "downloading"
	FilePaused      FileState = "paused"
	FileVerifying   FileState = "verifying"
	FileDone        FileState = "done"
	FileFailed      FileState = "failed" // checksum or network failure, with Err set
	FileOffline     FileState = "offline"
)

// DownloadProgress is one update for one file. Publishers send at most 4 updates per second per file.
type DownloadProgress struct {
	Key         string    // Asset.Key(), the identity every Downloader command takes
	Name        string    // Asset.Name, for display
	State       FileState // the current state
	Done, Total int64     // bytes
	BytesPerSec float64
	ResumedFrom int64  // bytes kept from an earlier run; set for a few seconds after a resume ("Resumed at 612 MB")
	Err         string // set when State is FileFailed
}

// Downloader fetches assets with resume, verification and pause (SPEC section 7). Files are keyed by
// Asset.Key(), so an asset shared by two combinations downloads once, and a new pin downloads again.
//
// Lifetime: downloads run under the Downloader's own lifetime, set when it is constructed. The ctx of
// a command only bounds that command's call.
//
// Repeated Ensure with the same descriptor does nothing to an asset that is already tracked. It keeps
// queued, downloading, paused, offline and verifying files as they are, keeps done files done, and
// keeps failed files failed until Retry. Ensure with a key that is tracked under a different descriptor
// returns ErrConflict, and then queues nothing from that call.
type Downloader interface {
	// Ensure queues the assets that are not yet verified on disk. Calling it again is safe (see above).
	Ensure(ctx context.Context, assets []Asset) error
	// Pause pauses one file by key. ErrUnknownAsset when Ensure has not queued it.
	Pause(key string) error
	// Resume resumes one paused file from the bytes it has.
	Resume(key string) error
	// PauseAll pauses every file ("Pause all").
	PauseAll()
	// ResumeAll resumes every paused file.
	ResumeAll()
	// Retry queues a failed file again, from zero.
	Retry(ctx context.Context, key string) error
	// Subscribe streams updates for all files, keyed by Key. Done and failed are terminal.
	Subscribe(ctx context.Context) <-chan DownloadProgress
}

// GPU is a graphics adapter found by the OS.
type GPU struct {
	Name, Vendor        string // Vendor is "amd", "nvidia", "intel" or "other" (SPEC section 5)
	Driver              string // empty when unknown
	VRAMTotal, VRAMFree int64  // bytes; 0 when unknown
}

// Device is one device that llama-server --list-devices lists for a backend.
type Device struct {
	Name                string
	VRAMTotal, VRAMFree int64 // bytes
}

// Hardware is what detection finds on this computer (SPEC section 5).
type Hardware struct {
	CPU                    string
	Threads                int
	RAMTotal, RAMAvailable int64 // bytes
	GPUs                   []GPU
	NvidiaDriver           string // from /proc/driver/nvidia/version or DXGI; empty when absent
}

// BackendProbe is the result of running llama-server --list-devices for one backend.
type BackendProbe struct {
	Backend   Backend
	Candidate bool     // true when the probe lists at least one device; the CPU backend is always a candidate
	Devices   []Device // empty for the CPU backend
	Reason    string   // why it is not a candidate, such as "no NVIDIA GPU"
}

// Detector finds the hardware and asks each downloaded backend which devices it has.
type Detector interface {
	// Detect reads the CPU, RAM and GPUs of this computer.
	Detect(ctx context.Context) (Hardware, error)
	// Probe runs llama-server --list-devices for backend b. It needs the backend's build on disk.
	Probe(ctx context.Context, b Backend) (BackendProbe, error)
}

// LabState is the state of one run of the test lab.
type LabState string

// The lab states, which the test table shows in its row.
const (
	LabQueued    LabState = "queued"
	LabStarting  LabState = "starting"
	LabEmbedding LabState = "embedding"
	LabDone      LabState = "done"
	LabFailed    LabState = "failed"
	LabCanceled  LabState = "canceled"
)

// RunID identifies one run of the test lab. Runs get IDs in the order they are started.
type RunID int64

// LabStep is one update of a test run.
type LabStep struct {
	RunID  RunID
	Combo  Combo
	State  LabState
	Step   string     // the live step, such as "Embedding image 3 of 8"
	Result *LabResult // set when State is LabDone
	Err    string     // set when State is LabFailed
}

// LabResult is one row of the test table (SPEC section 6, step 2).
type LabResult struct {
	RunID RunID
	Combo Combo
	Bench BenchResult
}

// Lab runs the test lab: it starts a combination, embeds the bundled sample and records the numbers.
//
// Runs are serialized: a run starts when the run before it has finished, and a run canceled while it
// waits never starts. Each run embeds in its own temporary session, opened with Runtime.Session for the
// vector space of its combination, and the indexer is paused with PauseLab while the run uses the
// runtime. A run that cannot open its session fails with that error. Every step and result carries its
// RunID. A run's ctx bounds that run: when it ends, the run emits a canceled step.
type Lab interface {
	// Run queues a test of combination c. extra holds up to 10 user-picked files to embed as well. It returns
	// the run's ID at once; progress arrives on Subscribe. It returns ErrUnknownCombo for a combination the
	// lab cannot test, and ErrStopped after the lab's lifetime ends.
	Run(ctx context.Context, c Combo, extra []string) (RunID, error)
	// Subscribe streams the steps of all runs, keyed by RunID. Done, failed and canceled are terminal.
	Subscribe(ctx context.Context) <-chan LabStep
	// Recommend returns the fastest result whose quality is at least 0.99. Results with n/a quality do not qualify.
	Recommend(results []LabResult) (Combo, bool)
}

// RuntimeState is the state of the sidecar.
type RuntimeState string

// The sidecar states (SPEC section 8, Q23).
const (
	RuntimeStarting    RuntimeState = "starting"
	RuntimeReady       RuntimeState = "ready"
	RuntimeRestarting  RuntimeState = "restarting"
	RuntimeCPUFallback RuntimeState = "cpu-fallback"
	RuntimeStopped     RuntimeState = "stopped"
	RuntimeFailed      RuntimeState = "failed"
)

// RuntimeStatus is the state of the sidecar and the combination it runs.
type RuntimeStatus struct {
	State   RuntimeState
	Combo   Combo
	Message string // for example "GPU backend failed twice; indexing continues on the CPU"
	Since   time.Time
}

// Session embeds text and images in one vector space. Every vector it returns belongs to Space, and callers
// search and store those vectors only in a generation whose Space equals it.
type Session interface {
	Embedder
	// Space returns the vector space of the session.
	Space() VectorSpace
	// Close releases the session. The sidecar of its space stops when no session of that space is open.
	Close() error
}

// Runtime runs the sidecars. The backend (cpu, vulkan, cuda) is runtime-wide. The runtime keeps one
// sidecar per open vector space, and at most two spaces are open: the active space and, during a rebuild,
// the building space. Embedding goes through a Session.
//
// Readiness: a session embeds only while its sidecar is ready (or on the CPU fallback). A request that
// is in flight while the sidecar starts or restarts fails with ErrRestarting, and the caller retries after
// Use returns. A session whose sidecar is stopped or failed fails with ErrNotReady.
type Runtime interface {
	// Active returns the combination the sidecar runs, or the one it is starting.
	Active() Combo
	// Status returns the current status.
	Status() RuntimeStatus
	// Subscribe streams status changes. Stopped and failed are terminal.
	Subscribe(ctx context.Context) <-chan RuntimeStatus
	// Session opens a session for vector space s. Opening a space that is open shares its sidecar.
	// It returns ErrTooManySpaces when two other spaces are open.
	Session(ctx context.Context, s VectorSpace) (Session, error)
	// Use makes the backend of c the backend of every sidecar, and restarts the open ones ("Make active").
	// It returns when every open session is ready again, or when ctx ends. ctx also bounds the restart:
	// when it ends first, the restart is abandoned and the sidecar is stopped. A Use that a later Use or
	// Stop supersedes returns ErrRestarting. With no session open, Use returns at once.
	Use(ctx context.Context, c Combo) error
	// RetryGPU tries the GPU backend again after a CPU fallback ("Try GPU again").
	RetryGPU(ctx context.Context) error
	// Stop stops every sidecar when the app quits. It ends a pending restart, and sessions fail with
	// ErrNotReady until Use starts the sidecar again.
	Stop(ctx context.Context) error
}
