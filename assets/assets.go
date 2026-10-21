// Package assets holds the bundled test-lab sample (SPEC section 6, step 2): 8 self-made images and 16 text
// chunks, and the BF16 reference vectors for them, which tools/mkref computes at release time.
package assets

import (
	"embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"path"
	"sort"
)

//go:embed sample
var sampleFS embed.FS

// Dims is the embedding size of EmbeddingGemma 2.
const Dims = 768

// Image is one sample image.
type Image struct {
	Name string
	Data []byte
	MIME string // image/png
}

// Chunk is one sample text chunk. Name is its file name, which is also the title in the document prompt.
type Chunk struct {
	Name string
	Text string
}

// Reference is the BF16 reference for the sample: one vector per item, images first, then chunks, in name order.
type Reference struct {
	Model   string   // the BF16 text model file name
	Backend string   // the backend that computed it
	Build   string   // the llama.cpp release tag
	Items   []string // the item names, in order
	Vectors [][]float32
}

// Images returns the sample images in name order.
func Images() ([]Image, error) {
	names, err := listSorted("sample/images")
	if err != nil {
		return nil, err
	}
	var out []Image
	for _, n := range names {
		b, err := sampleFS.ReadFile(path.Join("sample/images", n))
		if err != nil {
			return nil, err
		}
		out = append(out, Image{Name: n, Data: b, MIME: "image/png"})
	}
	return out, nil
}

// Chunks returns the sample text chunks in name order.
func Chunks() ([]Chunk, error) {
	names, err := listSorted("sample/text")
	if err != nil {
		return nil, err
	}
	var out []Chunk
	for _, n := range names {
		b, err := sampleFS.ReadFile(path.Join("sample/text", n))
		if err != nil {
			return nil, err
		}
		out = append(out, Chunk{Name: n, Text: string(b)})
	}
	return out, nil
}

// LoadReference returns the BF16 reference vectors. The manifest is reference.json and the vectors are
// reference.bin, little-endian float32, one row of Dims values per item.
func LoadReference() (Reference, error) {
	var ref Reference
	mb, err := sampleFS.ReadFile("sample/reference.json")
	if err != nil {
		return ref, fmt.Errorf("assets: reference manifest: %w", err)
	}
	var m struct {
		Model   string   `json:"model"`
		Backend string   `json:"backend"`
		Build   string   `json:"build"`
		Dims    int      `json:"dims"`
		Items   []string `json:"items"`
	}
	if err := json.Unmarshal(mb, &m); err != nil {
		return ref, err
	}
	if m.Dims != Dims {
		return ref, fmt.Errorf("assets: reference has %d dims, want %d", m.Dims, Dims)
	}
	bin, err := sampleFS.ReadFile("sample/reference.bin")
	if err != nil {
		return ref, fmt.Errorf("assets: reference vectors: %w", err)
	}
	if len(bin) != len(m.Items)*Dims*4 {
		return ref, fmt.Errorf("assets: reference.bin has %d bytes for %d items", len(bin), len(m.Items))
	}
	ref = Reference{Model: m.Model, Backend: m.Backend, Build: m.Build, Items: m.Items}
	for i := range m.Items {
		row := make([]float32, Dims)
		for d := range row {
			row[d] = math.Float32frombits(binary.LittleEndian.Uint32(bin[(i*Dims+d)*4:]))
		}
		ref.Vectors = append(ref.Vectors, row)
	}
	return ref, nil
}

// WriteReference is used by tools/mkref: it returns the manifest and the vector bytes for ref.
func EncodeReference(ref Reference) ([]byte, []byte, error) {
	m := map[string]any{"model": ref.Model, "backend": ref.Backend, "build": ref.Build, "dims": Dims, "items": ref.Items}
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	bin := make([]byte, 0, len(ref.Vectors)*Dims*4)
	for _, v := range ref.Vectors {
		if len(v) != Dims {
			return nil, nil, fmt.Errorf("assets: vector has %d dims", len(v))
		}
		for _, x := range v {
			bin = binary.LittleEndian.AppendUint32(bin, math.Float32bits(x))
		}
	}
	return append(mb, '\n'), bin, nil
}

func listSorted(dir string) ([]string, error) {
	entries, err := fs.ReadDir(sampleFS, dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
