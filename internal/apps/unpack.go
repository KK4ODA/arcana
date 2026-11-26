package apps

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
	"slices"
	"sort"
	"strings"
)

// Packed is one installed file of an app's resource, on the drive.
type Packed struct {
	// Name is the file's name in the resource (part_platform_rest).
	Name string
	// Path is where it is on the drive.
	Path   string
	SHA256 string
}

// Select returns the files of an app this platform needs: those for "any"
// platform and those for this one. It fails with ErrNotForPlatform when a
// part this platform needs is published only for other platforms.
func Select(files []Packed, platform string, parts map[string]Part) ([]Packed, error) {
	type partFiles struct{ any, mine, other bool }
	found := map[string]*partFiles{}
	var out []Packed
	for _, f := range files {
		n, err := ParseFileName(f.Name)
		if err != nil {
			return nil, err
		}
		pf := found[n.Part]
		if pf == nil {
			pf = &partFiles{}
			found[n.Part] = pf
		}
		switch n.Platform {
		case "any":
			pf.any = true
			out = append(out, f)
		case platform:
			pf.mine = true
			out = append(out, f)
		default:
			pf.other = true
		}
	}
	if len(found) == 0 {
		return nil, errors.New("the app has no files")
	}
	for name, pf := range found {
		if !pf.any && !pf.mine && !slices.Contains(parts[name].NotNeededOn, platform) {
			return nil, fmt.Errorf("%w (%s has none for %s)", ErrNotForPlatform, name, platform)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Platforms returns the platforms an app's files cover: nil when every
// part is for any platform.
func Platforms(files []Packed, parts map[string]Part) []string {
	seen := map[string]bool{}
	for _, f := range files {
		if n, err := ParseFileName(f.Name); err == nil && n.Platform != "any" {
			seen[n.Platform] = true
		}
	}
	var out []string
	for p := range seen {
		if _, err := Select(files, p, parts); err == nil {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

const completeMarker = ".arcana-complete"

// Unpack makes an app's files for this platform ready under root (the
// computer's cache folder) and returns the folder. Each part goes into a
// folder of its own name. A folder is used only once a marker written
// after its last file says it is complete; an interrupted attempt is
// removed and tried again. A new version unpacks beside the old one.
func Unpack(spec *Spec, files []Packed, root string) (string, error) {
	h := sha256.New()
	fmt.Fprintln(h, spec.ID)
	for _, f := range files {
		fmt.Fprintln(h, f.Name, f.SHA256)
	}
	dir := filepath.Join(root, spec.ID+"-"+hex.EncodeToString(h.Sum(nil)[:6]))
	if _, err := os.Stat(filepath.Join(dir, completeMarker)); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	_ = os.RemoveAll(dir)
	tmp, err := os.MkdirTemp(root, spec.ID+"-unpacking-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	for _, f := range files {
		n, err := ParseFileName(f.Name)
		if err != nil {
			return "", err
		}
		part := spec.Parts[n.Part]
		into := filepath.Join(tmp, n.Part)
		if err := unpackFile(f.Path, n.Rest, into, part); err != nil {
			return "", fmt.Errorf("unpack %s: %w", f.Name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, completeMarker), []byte(spec.ID+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	ok = true
	return dir, nil
}

// CleanOld removes an app's unpacked folders under root other than keep.
func CleanOld(spec *Spec, root, keep string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), spec.ID+"-") {
			continue
		}
		// Another app's ID may start with this one's ("calibre" and
		// "calibre-web"): the rest must be a hash or "unpacking-…".
		rest := strings.TrimPrefix(e.Name(), spec.ID+"-")
		if len(rest) != 12 && !strings.HasPrefix(rest, "unpacking-") {
			continue
		}
		if p := filepath.Join(root, e.Name()); p != keep {
			os.RemoveAll(p)
		}
	}
}

// decompressors open compressed tar archives by extension. Formats beyond
// the standard library's register themselves here.
var decompressors = map[string]func(io.Reader) (io.Reader, error){
	".tar.gz": func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) },
	".tgz":    func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) },
	".tar":    func(r io.Reader) (io.Reader, error) { return r, nil },
}

func unpackFile(file, rest, into string, part Part) error {
	if err := os.MkdirAll(into, 0o755); err != nil {
		return err
	}
	lower := strings.ToLower(rest)
	if !part.Copy {
		if strings.HasSuffix(lower, ".zip") {
			return unzip(file, into, part)
		}
		for ext, open := range decompressors {
			if strings.HasSuffix(lower, ext) {
				return untar(file, into, open, part)
			}
		}
	}
	dst, err := within(into, rest)
	if err != nil {
		return err
	}
	return copyFile(file, dst)
}

// within joins name to dir, refusing names that leave it.
func within(dir, name string) (string, error) {
	name = path.Clean(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimPrefix(name, "./")
	if name == "." || name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) || strings.Contains(name, ":") {
		return "", fmt.Errorf("unsafe path %q in archive", name)
	}
	return filepath.Join(dir, filepath.FromSlash(name)), nil
}

// maxFile bounds one decompressed file, against archives that expand
// without limit.
const maxFile = 4 << 30

func untar(file, dir string, open func(io.Reader) (io.Reader, error), part Part) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := open(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(r)
	type link struct {
		at, target string
		hard       bool
	}
	var links []link
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path.Clean(strings.ReplaceAll(h.Name, "\\", "/")), "./")
		if name == "." || name == "" {
			continue
		}
		// Container image layers mark deleted files with ".wh." entries.
		if strings.HasPrefix(path.Base(name), ".wh.") {
			continue
		}
		dst, err := within(dir, name)
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
			if err := writeEntry(dst, tr, mode, part); err != nil {
				return err
			}
		case tar.TypeSymlink:
			target := path.Join(path.Dir(name), h.Linkname)
			if path.IsAbs(h.Linkname) {
				return fmt.Errorf("unsafe link %q in archive", h.Name)
			}
			if _, err := within(dir, target); err != nil {
				return fmt.Errorf("unsafe link %q in archive", h.Name)
			}
			links = append(links, link{at: dst, target: h.Linkname})
		case tar.TypeLink:
			target, err := within(dir, h.Linkname)
			if err != nil {
				return fmt.Errorf("unsafe link %q in archive", h.Name)
			}
			links = append(links, link{at: dst, target: target, hard: true})
		default:
			// Devices, FIFOs and the like are not expected; skip them.
		}
	}
	for _, l := range links {
		if err := os.MkdirAll(filepath.Dir(l.at), 0o755); err != nil {
			return err
		}
		if l.hard {
			if err := copyFile(l.target, l.at); err != nil {
				return err
			}
			continue
		}
		_ = os.Remove(l.at)
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

func unzip(file, dir string, part Part) error {
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
		if strings.HasSuffix(zf.Name, "/") || zf.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		if zf.Mode()&os.ModeSymlink != 0 {
			continue // not expected in the apps' archives
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if zf.Mode()&0o111 != 0 {
			mode = 0o755
		}
		err = writeEntry(dst, rc, mode, part)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeEntry writes one archive member, decompressing it first when the
// part asks for it.
func writeEntry(dst string, r io.Reader, mode os.FileMode, part Part) error {
	if part.Gunzip && strings.HasSuffix(dst, ".gz") {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer zr.Close()
		return writeFile(strings.TrimSuffix(dst, ".gz"), zr, mode)
	}
	return writeFile(dst, r, mode)
}

func writeFile(dst string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(r, maxFile+1))
	if err == nil && n > maxFile {
		err = fmt.Errorf("%s is larger than %d bytes", filepath.Base(dst), int64(maxFile))
	}
	if err != nil {
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
