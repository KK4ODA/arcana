package kolibri

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Packed names the two files of the arcana.kolibri resource that one
// computer needs: Kolibri's wheel and the Python runtime for its platform.
// Both stay packed on the drive (ADR-0024).
type Packed struct {
	Wheel       string
	WheelSHA256 string
	Python      string
	// PythonSHA256 identifies the runtime; with WheelSHA256 it names the
	// unpacked folder, so a new version unpacks beside the old one.
	PythonSHA256 string
}

// Runtime is Kolibri unpacked on this computer.
type Runtime struct {
	// Python is the interpreter.
	Python string
	// App is the folder holding the kolibri package (put on PYTHONPATH).
	App string
	// Dir is the folder both were unpacked into.
	Dir string
}

// PythonFileFor returns the file name of the Python runtime for a platform
// ("linux-amd64"), as named in the arcana.kolibri resource.
func PythonFileFor(platform string) (prefix, suffix string) {
	return "python-", "-" + platform + ".tar.gz"
}

// IsPythonFor reports whether name is the Python runtime for platform.
func IsPythonFor(name, platform string) bool {
	prefix, suffix := PythonFileFor(platform)
	return strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix)
}

const completeMarker = ".arcana-complete"

// Unpack makes the runtime ready under root (normally the computer's cache
// folder) and returns it. A folder is used only once a marker written after
// its last file says it is complete; an interrupted attempt is removed and
// tried again.
func Unpack(p Packed, root string) (Runtime, error) {
	if p.Wheel == "" || p.Python == "" {
		return Runtime{}, errors.New("the Kolibri runtime is not installed for this computer")
	}
	key := sha256.Sum256([]byte(p.WheelSHA256 + "\n" + p.PythonSHA256 + "\n" + filepath.Base(p.Wheel) + filepath.Base(p.Python)))
	dir := filepath.Join(root, "kolibri-"+hex.EncodeToString(key[:6]))
	rt := runtimeIn(dir)
	if _, err := os.Stat(filepath.Join(dir, completeMarker)); err == nil {
		return rt, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Runtime{}, err
	}
	_ = os.RemoveAll(dir)
	tmp, err := os.MkdirTemp(root, "kolibri-unpacking-")
	if err != nil {
		return Runtime{}, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	if err := untarGz(p.Python, tmp); err != nil {
		return Runtime{}, fmt.Errorf("unpack Python: %w", err)
	}
	if err := unzip(p.Wheel, filepath.Join(tmp, "app")); err != nil {
		return Runtime{}, fmt.Errorf("unpack Kolibri: %w", err)
	}
	if _, err := os.Stat(runtimeIn(tmp).Python); err != nil {
		return Runtime{}, errors.New("the Python runtime has no interpreter for this computer")
	}
	if err := os.WriteFile(filepath.Join(tmp, completeMarker), []byte(p.WheelSHA256+"\n"+p.PythonSHA256+"\n"), 0o644); err != nil {
		return Runtime{}, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return Runtime{}, err
	}
	ok = true
	return rt, nil
}

func runtimeIn(dir string) Runtime {
	py := filepath.Join(dir, "python", "bin", "python3")
	if runtime.GOOS == "windows" {
		py = filepath.Join(dir, "python", "python.exe")
	}
	return Runtime{Python: py, App: filepath.Join(dir, "app"), Dir: dir}
}

// CleanOld removes unpacked runtimes under root other than keep.
func CleanOld(root, keep string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || !(strings.HasPrefix(n, "kolibri-")) {
			continue
		}
		if p := filepath.Join(root, n); p != keep {
			os.RemoveAll(p)
		}
	}
}

// within joins name to dir, refusing names that leave it.
func within(dir, name string) (string, error) {
	name = path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || strings.HasPrefix(name, "../") || name == ".." || path.IsAbs(name) || strings.Contains(name, ":") {
		return "", fmt.Errorf("unsafe path %q in archive", name)
	}
	return filepath.Join(dir, filepath.FromSlash(name)), nil
}

func untarGz(file, dir string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	type link struct{ at, target string }
	var links []link
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		dst, err := within(dir, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			if err := writeFile(dst, tr, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Interpreter links (bin/python3 -> python3.11) must stay inside.
			target := path.Join(path.Dir(strings.ReplaceAll(h.Name, "\\", "/")), h.Linkname)
			if _, err := within(dir, target); err != nil || path.IsAbs(h.Linkname) {
				return fmt.Errorf("unsafe link %q in archive", h.Name)
			}
			links = append(links, link{at: dst, target: h.Linkname})
		default:
			// Hard links, devices and the like are not expected; skip.
		}
	}
	for _, l := range links {
		if err := os.MkdirAll(filepath.Dir(l.at), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(l.target, l.at); err != nil {
			// A file system without links: copy the target instead.
			src := filepath.Join(filepath.Dir(l.at), filepath.FromSlash(l.target))
			if err := copyFile(src, l.at); err != nil {
				return err
			}
		}
	}
	return nil
}

func unzip(file, dir string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		dst, err := within(dir, zf.Name)
		if err != nil {
			return err
		}
		if strings.HasSuffix(zf.Name, "/") {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(dst, rc, 0o644)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(dst string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	return writeFile(dst, in, st.Mode().Perm())
}
