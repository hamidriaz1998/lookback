package store

import (
	"os"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/testlib"
)

func TestMain(m *testing.M) {
	if os.Getenv(childRootEnv) == "" { // the child of TestOpenUsesDataRootLib checks the default folder, <root>/lib
		testlib.UseSharedTursoLibrary("store")
	}
	os.Exit(m.Run())
}
