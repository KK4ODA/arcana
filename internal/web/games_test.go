package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBoardGameRules runs the board games' rule tests (testdata/games) with
// Node's built-in test runner. The rule modules are plain ES modules with no
// dependencies; they are copied next to the tests into a folder marked as
// ES modules, so any Node from version 18 runs them without flags or npm.
// The test is skipped where Node is not installed.
func TestBoardGameRules(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the board game rules are not tested")
	}
	dir := t.TempDir()
	copyAll := func(pattern string) []string {
		files, err := filepath.Glob(pattern)
		if err != nil || len(files) == 0 {
			t.Fatalf("no files match %s", pattern)
		}
		var names []string
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Base(f)
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		return names
	}
	copyAll(filepath.Join("static", "js", "games", "*.js"))
	tests := copyAll(filepath.Join("testdata", "games", "*.test.mjs"))
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, append([]string{"--test"}, tests...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("board game rule tests failed: %v\n%s", err, out)
	}
	if testing.Verbose() {
		t.Log(strings.TrimSpace(string(out)))
	}
}
