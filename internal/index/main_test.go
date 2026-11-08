package index

import (
	"os"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/testlib"
)

func TestMain(m *testing.M) {
	testlib.UseSharedTursoLibrary("index")
	os.Exit(m.Run())
}
