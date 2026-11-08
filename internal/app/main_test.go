package app

import (
	"os"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/testlib"
)

func TestMain(m *testing.M) {
	testlib.UseSharedTursoLibrary("app")
	os.Exit(m.Run())
}
