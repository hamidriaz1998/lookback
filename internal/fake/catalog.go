package fake

import (
	"fmt"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

const (
	runtimeTag = "b11755"
	runtimeURL = "https://github.com/ggml-org/llama.cpp/releases/download/" + runtimeTag + "/"
	modelURL   = "https://huggingface.co/ggml-org/embeddinggemma-2-GGUF/resolve/" + modelRev + "/"
	mb         = int64(1_000_000)
)

// Catalog is a contract.Catalog with the pins from SPEC section 4. Model files have their exact
// size and SHA-256. Runtime archives have approximate sizes and no SHA-256 yet: WP1 pins them.
type Catalog struct{}

// NewCatalog returns the fake catalog.
func NewCatalog() *Catalog { return &Catalog{} }

// runtimeFiles lists the archives of each backend on each OS.
var runtimeFiles = map[contract.OS]map[contract.Backend][]contract.Asset{
	"linux": {
		contract.BackendCPU:    {{Name: "llama-" + runtimeTag + "-bin-ubuntu-x64.tar.gz", Bytes: 22 * mb}},
		contract.BackendVulkan: {{Name: "llama-" + runtimeTag + "-bin-ubuntu-vulkan-x64.tar.gz", Bytes: 34 * mb}},
		contract.BackendCUDA12: {
			{Name: "llama-" + runtimeTag + "-bin-ubuntu-cuda-12.8-x64.tar.gz", Bytes: 177 * mb},
			{Name: "cudart-llama-" + runtimeTag + "-bin-ubuntu-cuda-12.8-x64.tar.gz", Bytes: 594 * mb},
		},
		contract.BackendCUDA13: {
			{Name: "llama-" + runtimeTag + "-bin-ubuntu-cuda-13.4-x64.tar.gz", Bytes: 158 * mb},
			{Name: "cudart-llama-" + runtimeTag + "-bin-ubuntu-cuda-13.4-x64.tar.gz", Bytes: 440 * mb},
		},
	},
	"windows": {
		contract.BackendCPU:    {{Name: "llama-" + runtimeTag + "-bin-win-cpu-x64.zip", Bytes: 23 * mb}},
		contract.BackendVulkan: {{Name: "llama-" + runtimeTag + "-bin-win-vulkan-x64.zip", Bytes: 36 * mb}},
		contract.BackendCUDA12: {
			{Name: "llama-" + runtimeTag + "-bin-win-cuda-12.4-x64.zip", Bytes: 271 * mb},
			{Name: "cudart-llama-bin-win-cuda-12.4-x64.zip", Bytes: 391 * mb},
		},
		contract.BackendCUDA13: {
			{Name: "llama-" + runtimeTag + "-bin-win-cuda-13.4-x64.zip", Bytes: 159 * mb},
			{Name: "cudart-llama-bin-win-cuda-13.4-x64.zip", Bytes: 423 * mb},
		},
	},
}

// modelFiles maps a precision to its text model and encoder files (SPEC section 4).
var modelFiles = map[contract.Quant]struct {
	text, enc contract.Asset
}{
	contract.QuantQ8_0: {
		text: contract.Asset{Name: "embeddinggemma-2-Q8_0.gguf", Bytes: 309855456,
			SHA256: "2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135"},
		enc: contract.Asset{Name: "mmproj-embeddinggemma-2-Q8_0.gguf", Bytes: 554821024,
			SHA256: "c4a8a52691ecef40618438928bdf9e68379b854e24166f292592353db0aab64f"},
	},
	contract.QuantBF16: {
		text: contract.Asset{Name: "embeddinggemma-2-BF16.gguf", Bytes: 557950176,
			SHA256: "68bae29d62fb8c7d23e98d21fd4662753ddd636e6b62b8a70dfe059a9844f216"},
		enc: contract.Asset{Name: "mmproj-embeddinggemma-2-BF16.gguf", Bytes: 982074784,
			SHA256: "d2033b3cd0223cfb2e7a2b50dc80a664c78c816d375f5b017ec3ef80ee0bc766"},
	},
}

// Assets returns the runtime archives and model files of combination c on os.
func (*Catalog) Assets(os contract.OS, c contract.Combo) ([]contract.Asset, error) {
	backends, ok := runtimeFiles[os]
	if !ok {
		return nil, fmt.Errorf("catalog: unknown OS %q", os)
	}
	rt, ok := backends[c.Backend]
	m, okText := modelFiles[c.TextQuant]
	e, okEnc := modelFiles[c.EncQuant]
	if !ok || !okText || !okEnc {
		return nil, contract.ErrUnknownCombo
	}
	var out []contract.Asset
	for _, a := range rt {
		a.URL = runtimeURL + a.Name
		a.Part = contract.PartRuntime
		a.Backend = c.Backend
		a.Archive = true
		out = append(out, a)
	}
	text := m.text
	text.URL, text.Part, text.Quant = modelURL+text.Name, contract.PartText, c.TextQuant
	enc := e.enc
	enc.URL, enc.Part, enc.Quant = modelURL+enc.Name, contract.PartEncoder, c.EncQuant
	return append(out, text, enc), nil
}

// Backends returns the backends the release offers on os.
func (*Catalog) Backends(os contract.OS) []contract.Backend {
	if _, ok := runtimeFiles[os]; !ok {
		return nil
	}
	return []contract.Backend{contract.BackendCPU, contract.BackendVulkan, contract.BackendCUDA12, contract.BackendCUDA13}
}
