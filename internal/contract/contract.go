// Package contract holds the types and interfaces that Lookback packages share.
// Packages talk through these interfaces; each one has a fake in internal/fake.
// Changing an interface needs an approved change here that updates the fakes too.
package contract

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
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

// The precisions that Lookback offers. F16 is never offered (SPEC §4).
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

// BenchResult is one row of the test lab (SPEC §6, step 2) for an installed combination.
type BenchResult struct {
	ImageMS          float64  // median wall time per image request, in milliseconds
	TextTokensPerSec float64  // usage.prompt_tokens divided by wall time over the text chunks
	PeakRAM          int64    // bytes
	VRAM             int64    // bytes, approximate; 0 for the CPU backend
	Quality          *float64 // mean cosine against the BF16 reference; nil means n/a
}

// Installed is a combination whose parts are on disk, with its latest test result if it has one.
type Installed struct {
	Combo       Combo
	InstalledAt time.Time
	Bench       *BenchResult // nil until the lab has tested it
}

// Kind is the type of a file, as SPEC §9 groups them.
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
	Size, MTime  int64 // bytes; Unix seconds
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
	ID, Gen, FileID int64
	Unit            int
	Start, End      int       // byte range in the text; -1 for images
	Modality        string    // "text" or "image"
	Snippet         string    // the first 300 characters, whitespace collapsed
	Vector          []float32 // 768 values, L2-normalized
}

// Hit is one search result: the best item of a file.
type Hit struct {
	ItemID, FileID int64
	Path           string
	Kind           Kind
	Modality       string // "text" or "image"; PDF pages rendered as images have Kind pdf and Modality image
	Unit           int
	Snippet        string
	Score          float64
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
)

// Progress is a snapshot of the indexing job. Publishers send at most 4 per second.
type Progress struct {
	FilesDone, FilesTotal     int
	FilesPerSec, TokensPerSec float64
	ETA                       time.Duration // negative while estimating
	Current                   string        // the file being processed
	PerFolder                 []FolderProgress
	Errors                    int
	Paused                    PauseReason
}

// Token is one token of a text, from Embedder.Tokenize. Start and End are the byte range of the
// token in the text, so text[Start:End] is its source. A token can cover part of a UTF-8 character:
// byte-fallback tokens do, and Piece then holds the raw bytes (see docs/spikes.md, spike 3).
type Token struct {
	ID         int32
	Piece      string
	Start, End int
}

// Embedder turns text and images into vectors. The sidecar implements it.
type Embedder interface {
	// EmbedTexts embeds prompts (each already has its prefix) and returns the vectors and the prompt tokens used.
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

// The generation states.
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
	Size, MTime int64
	Status      Status
	Error       string // set when Status is StatusError
	Tokens, Ms  int64
}

// Generation is one vector space: one precision pair (SPEC §10, Q26).
type Generation struct {
	ID                  int64
	TextQuant, EncQuant Quant
	ModelRev            string
	State               GenState
	CreatedAt           time.Time
}

// Store persists folders, files, items and settings (SPEC §10). Search and
// Similar read the items of one generation only.
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
	// RecentFiles returns the limit most recently modified files.
	RecentFiles(ctx context.Context, limit int) ([]FileRec, error)

	// ListFileIndex returns the file_index rows of one generation.
	ListFileIndex(ctx context.Context, gen int64) ([]FileIndex, error)
	// CommitFile replaces the items of idx.FileID in idx.Gen and upserts its file_index row,
	// in one transaction. Every item must have Gen set to idx.Gen.
	CommitFile(ctx context.Context, idx FileIndex, items []Item) error

	// NewGeneration inserts g and sets g.ID.
	NewGeneration(ctx context.Context, g *Generation) error
	// ListGenerations returns every generation, oldest first.
	ListGenerations(ctx context.Context) ([]Generation, error)
	// SetGenerationState changes the state of one generation.
	SetGenerationState(ctx context.Context, id int64, state GenState) error
	// DeleteItems removes the items and file_index rows of one generation.
	DeleteItems(ctx context.Context, gen int64) error

	// SaveCombo inserts or replaces an installed combination, keyed by Combo.ID.
	SaveCombo(ctx context.Context, in Installed) error
	// ListCombos returns every installed combination.
	ListCombos(ctx context.Context) ([]Installed, error)
	// DeleteCombo removes an installed combination.
	DeleteCombo(ctx context.Context, c Combo) error

	// Search returns the best item of each file in gen, nearest to query first, at most limit hits.
	// Hit.Score is the cosine similarity (1 minus the distance); the search package adds the filename boost.
	// kinds, when not empty, limits the files to those kinds.
	Search(ctx context.Context, gen int64, query []float32, kinds []Kind, limit int) ([]Hit, error)
	// Similar returns the files nearest to the stored vector of itemID, excluding its own file.
	Similar(ctx context.Context, gen int64, itemID int64, kinds []Kind, limit int) ([]Hit, error)

	// Close releases the database.
	Close() error
}

// Extractor turns one file into units. One Extractor per kind; the registry is in internal/extract.
type Extractor interface {
	// Extract streams the units of one file to emit, in order. Images come back already resized and encoded.
	Extract(ctx context.Context, f FileRec, emit func(Unit) error) error
}

// Indexer runs the indexing job: queue, pipeline, pauses and progress.
type Indexer interface {
	// Start begins indexing in the background. It returns when the job is running.
	Start(ctx context.Context) error
	// Pause stops the job for reason. Pauses for different reasons stack.
	Pause(reason PauseReason)
	// Resume lifts the pause for reason.
	Resume(reason PauseReason)
	// Progress returns the channel of progress snapshots.
	Progress() <-chan Progress
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
