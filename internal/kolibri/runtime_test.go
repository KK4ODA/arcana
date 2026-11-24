package kolibri

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type entry struct {
	name, body, link string
	mode             int64
}

func writeTarGz(t *testing.T, p string, entries []entry) {
	t.Helper()
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link != "" {
			h = &tar.Header{Name: e.name, Linkname: e.link, Typeflag: tar.TypeSymlink, Mode: 0o777}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()
	f.Close()
}

func writeZip(t *testing.T, p string, files map[string]string) {
	t.Helper()
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	f.Close()
}

func TestUnpack(t *testing.T) {
	dir := t.TempDir()
	py := filepath.Join(dir, "python.tar.gz")
	interp := "python/bin/python3.11"
	entries := []entry{{name: interp, body: "#!python", mode: 0o755}, {name: "python/bin/python3", link: "python3.11"},
		{name: "python/lib/os.py", body: "# os", mode: 0o644}}
	if runtime.GOOS == "windows" {
		entries = []entry{{name: "python/python.exe", body: "MZ", mode: 0o755}}
	}
	writeTarGz(t, py, entries)
	wheel := filepath.Join(dir, "kolibri.whl")
	writeZip(t, wheel, map[string]string{"kolibri/__init__.py": "version = '0.19.5'", "kolibri-0.19.5.dist-info/METADATA": "Name: kolibri"})

	cache := filepath.Join(dir, "cache")
	packed := Packed{Wheel: wheel, WheelSHA256: "aa", Python: py, PythonSHA256: "bb"}
	rt, err := Unpack(packed, cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rt.App, "kolibri", "__init__.py")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(rt.Python)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode()&0o111 == 0 {
		t.Fatal("interpreter is not executable")
	}
	// A second call reuses the complete folder.
	os.Remove(py)
	if rt2, err := Unpack(packed, cache); err != nil || rt2 != rt {
		t.Fatalf("not reused: %v", err)
	}
	// Another version unpacks beside it; CleanOld removes the first.
	writeTarGz(t, py, entries)
	rt3, err := Unpack(Packed{Wheel: wheel, WheelSHA256: "cc", Python: py, PythonSHA256: "bb"}, cache)
	if err != nil || rt3.Dir == rt.Dir {
		t.Fatalf("new version: %v", err)
	}
	CleanOld(cache, rt3.Dir)
	if _, err := os.Stat(rt.Dir); !os.IsNotExist(err) {
		t.Fatal("old runtime kept")
	}
	if _, err := os.Stat(rt3.Dir); err != nil {
		t.Fatal("current runtime removed")
	}
}

func TestUnpackRefusesEscapes(t *testing.T) {
	dir := t.TempDir()
	wheel := filepath.Join(dir, "kolibri.whl")
	writeZip(t, wheel, map[string]string{"kolibri/__init__.py": ""})
	for name, entries := range map[string][]entry{
		"dot-dot":  {{name: "python/../../evil", body: "x", mode: 0o644}},
		"absolute": {{name: "/etc/evil", body: "x", mode: 0o644}},
		"link out": {{name: "python/bin/python3", link: "../../../../etc/passwd"}},
		"no python": {{name: "python/lib/os.py", body: "x", mode: 0o644}},
	} {
		py := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".tar.gz")
		writeTarGz(t, py, entries)
		cache := filepath.Join(dir, "cache-"+strings.ReplaceAll(name, " ", "-"))
		if _, err := Unpack(Packed{Wheel: wheel, WheelSHA256: "a", Python: py, PythonSHA256: name}, cache); err == nil {
			t.Errorf("%s: unpacked", name)
		}
		left, _ := os.ReadDir(cache)
		if len(left) != 0 {
			t.Errorf("%s: left %d entries behind", name, len(left))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("wrote outside the folder")
	}
}

func TestIsPythonFor(t *testing.T) {
	if !IsPythonFor("python-3.11.17-linux-amd64.tar.gz", "linux-amd64") || IsPythonFor("python-3.11.17-linux-arm64.tar.gz", "linux-amd64") ||
		IsPythonFor("kolibri-0.19.5-py2.py3-none-any.whl", "linux-amd64") {
		t.Fatal("IsPythonFor")
	}
}
