// Package catalog holds the pinned manifest of runtimes and model files, compiled into the binary
// with go:embed. It gives the URL, size and SHA-256 of every asset the app may download (SPEC section 4).
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

//go:embed catalog.json
var manifestJSON []byte

const (
	runtimeBase = "https://github.com/ggml-org/llama.cpp/releases/download/"
	modelBase   = "https://huggingface.co/ggml-org/embeddinggemma-2-GGUF/resolve/"
)

// entry is one pinned file as catalog.json names it.
type entry struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// manifest is the shape of catalog.json.
type manifest struct {
	Tag           string                                       `json:"tag"`
	ModelRevision string                                       `json:"model_revision"`
	Runtimes      map[contract.OS]map[contract.Backend][]entry `json:"runtimes"`
	Models        map[contract.Quant]struct{ Text, Enc entry } `json:"models"`
}

// Catalog is a contract.Catalog over the embedded manifest.
type Catalog struct {
	m manifest
}

var (
	loadOnce sync.Once
	loaded   manifest
	loadErr  error
)

// New parses the embedded manifest. It fails only when the build embeds a broken catalog.json.
func New() (*Catalog, error) {
	loadOnce.Do(func() {
		loadErr = json.Unmarshal(manifestJSON, &loaded)
	})
	if loadErr != nil {
		return nil, fmt.Errorf("catalog: %w", loadErr)
	}
	return &Catalog{m: loaded}, nil
}

var _ contract.Catalog = (*Catalog)(nil)

// backendOrder is the order Backends returns.
var backendOrder = []contract.Backend{contract.BackendCPU, contract.BackendVulkan, contract.BackendCUDA12, contract.BackendCUDA13}

// Assets returns the runtime archives of c's backend and the text and encoder files of its precisions.
func (c *Catalog) Assets(os contract.OS, combo contract.Combo) ([]contract.Asset, error) {
	backends, ok := c.m.Runtimes[os]
	if !ok {
		return nil, fmt.Errorf("catalog: no runtimes for %q", os)
	}
	rt, okRT := backends[combo.Backend]
	models, okText := c.m.Models[combo.TextQuant]
	_, okEnc := c.m.Models[combo.EncQuant]
	if !okRT || !okText || !okEnc {
		return nil, contract.ErrUnknownCombo
	}
	var out []contract.Asset
	for _, e := range rt {
		out = append(out, contract.Asset{
			Name: e.Name, URL: runtimeBase + c.m.Tag + "/" + e.Name, Bytes: e.Bytes, SHA256: e.SHA256,
			Part: contract.PartRuntime, Backend: combo.Backend, Archive: true,
		})
	}
	mdl := func(e entry, part contract.Part, q contract.Quant) contract.Asset {
		return contract.Asset{Name: e.Name, URL: modelBase + c.m.ModelRevision + "/" + e.Name,
			Bytes: e.Bytes, SHA256: e.SHA256, Part: part, Quant: q}
	}
	out = append(out, mdl(models.Text, contract.PartText, combo.TextQuant))
	out = append(out, mdl(c.m.Models[combo.EncQuant].Enc, contract.PartEncoder, combo.EncQuant))
	return out, nil
}

// Backends returns the backends the release offers on os, in the order of backendOrder.
func (c *Catalog) Backends(os contract.OS) []contract.Backend {
	var out []contract.Backend
	for _, b := range backendOrder {
		if _, ok := c.m.Runtimes[os][b]; ok {
			out = append(out, b)
		}
	}
	return out
}
