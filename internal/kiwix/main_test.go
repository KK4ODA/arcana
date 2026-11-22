package kiwix

import (
	"os"
	"strconv"
	"testing"
)

// TestMain lets the test binary stand in for kiwix-serve: with
// ARCANA_TEST_KIWIX_EXIT set it exits at once with that status, the way a
// program the system cannot load does.
func TestMain(m *testing.M) {
	if code := os.Getenv("ARCANA_TEST_KIWIX_EXIT"); code != "" {
		n, _ := strconv.ParseUint(code, 0, 32)
		os.Exit(int(int32(uint32(n))))
	}
	os.Exit(m.Run())
}
