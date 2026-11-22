package kiwix

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestMissingRuntimeExplained(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARCANA_TEST_KIWIX_EXIT", "0xC0000135") // STATUS_DLL_NOT_FOUND
	s := New(exe, t.TempDir(), nil)
	_, _, err = s.launch(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "Visual C++") {
		t.Fatalf("launch error %v", err)
	}
}
