package runtime

import (
	"os"
	"slices"
	"testing"
)

// TestMain lets the test binary stand in for llama-server: the runtime starts it with --embeddings, and it then
// serves fakeLlama instead of running tests. The fake speaks the same HTTP API (SPEC section 8), so the process code
// runs under -short with no model on disk.
func TestMain(m *testing.M) {
	if os.Getenv("LOOKBACK_LIVE_PARENT") != "" { // the app of TestLiveSidecarDiesWithApp
		liveParent()
		os.Exit(0)
	}
	if slices.Contains(os.Args, "--embeddings") {
		fakeLlama(os.Args[1:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}
