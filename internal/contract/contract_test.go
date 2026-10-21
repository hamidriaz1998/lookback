package contract

import "testing"

// A file needs work when it has no row, or when its size or its nanosecond mtime differs. A same-size
// edit one nanosecond later must count, since whole-second mtimes missed it (review finding 7).
func TestNeedsWork(t *testing.T) {
	f := FileRec{ID: 1, Size: 4, MTime: 1_000_000_001}
	if !NeedsWork(f, nil) {
		t.Fatal("a file with no row needs work")
	}
	same := &FileIndex{FileID: 1, Size: 4, MTime: 1_000_000_001}
	if NeedsWork(f, same) {
		t.Fatal("an unchanged file needs no work")
	}
	subsecond := &FileIndex{FileID: 1, Size: 4, MTime: 1_000_000_000}
	if !NeedsWork(f, subsecond) {
		t.Fatal("a same-size edit one nanosecond later needs work")
	}
	resized := &FileIndex{FileID: 1, Size: 5, MTime: 1_000_000_001}
	if !NeedsWork(f, resized) {
		t.Fatal("a resized file needs work")
	}
}
