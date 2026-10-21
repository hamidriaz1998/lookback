package assets

import (
	"bytes"
	"image/png"
	"testing"
)

func TestSampleAndReferenceMatch(t *testing.T) {
	images, err := Images()
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := Chunks()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := LoadReference()
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 8 || len(chunks) != 16 || len(ref.Items) != len(images)+len(chunks) {
		t.Fatalf("sample has %d images, %d chunks, %d references", len(images), len(chunks), len(ref.Items))
	}
	for i, img := range images {
		if _, err := png.DecodeConfig(bytes.NewReader(img.Data)); err != nil {
			t.Fatalf("image %s: %v", img.Name, err)
		}
		if ref.Items[i] != img.Name {
			t.Fatalf("reference item %d is %q, want %q", i, ref.Items[i], img.Name)
		}
	}
	for i, chunk := range chunks {
		if ref.Items[len(images)+i] != chunk.Name {
			t.Fatalf("reference item %d is %q, want %q", len(images)+i, ref.Items[len(images)+i], chunk.Name)
		}
	}
	for i, v := range ref.Vectors {
		if len(v) != Dims {
			t.Fatalf("reference vector %d has %d dimensions, want %d", i, len(v), Dims)
		}
	}
}
