package search

import (
	"os"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/testlib"
)

func TestMain(m *testing.M) {
	testlib.UseSharedTursoLibrary("search")
	os.Exit(m.Run())
}
