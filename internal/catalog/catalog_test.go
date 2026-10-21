package catalog

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Every asset the app may download is pinned, and Linux never offers a Windows archive.
func TestLinuxAssetsArePinnedAndLinuxOnly(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range c.Backends("linux") {
		combo := contract.Combo{Backend: b, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}
		assets, err := c.Assets("linux", combo)
		if err != nil {
			t.Fatalf("%s: %v", b, err)
		}
		for _, a := range assets {
			if !sha256Hex.MatchString(a.SHA256) {
				t.Errorf("%s: %s has no SHA-256 pin", b, a.Name)
			}
			if a.Bytes <= 0 {
				t.Errorf("%s: %s has no size", b, a.Name)
			}
			if strings.Contains(a.Name, "win") {
				t.Errorf("%s: Windows archive %s offered on Linux", b, a.Name)
			}
		}
	}
}

// The CUDA combinations carry the llama.cpp archive and its cudart archive, plus the two model files.
func TestCUDAComboHasRuntimeAndCudart(t *testing.T) {
	c, _ := New()
	assets, err := c.Assets("linux", contract.Combo{Backend: contract.BackendCUDA13, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 4 {
		t.Fatalf("got %d assets, want 4 (runtime, cudart, text, encoder)", len(assets))
	}
	if assets[0].Name != "llama-b11755-bin-ubuntu-cuda-13.4-x64.tar.gz" || !assets[0].Archive {
		t.Errorf("first asset = %+v", assets[0])
	}
	if assets[1].Name != "cudart-llama-b11755-bin-ubuntu-cuda-13.4-x64.tar.gz" {
		t.Errorf("second asset = %s", assets[1].Name)
	}
	if want := "https://github.com/ggml-org/llama.cpp/releases/download/b11755/" + assets[0].Name; assets[0].URL != want {
		t.Errorf("URL = %s, want %s", assets[0].URL, want)
	}
}

func TestUnknownCombo(t *testing.T) {
	c, _ := New()
	_, err := c.Assets("linux", contract.Combo{Backend: "rocm", TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16})
	if !errors.Is(err, contract.ErrUnknownCombo) {
		t.Fatalf("err = %v, want ErrUnknownCombo", err)
	}
}
