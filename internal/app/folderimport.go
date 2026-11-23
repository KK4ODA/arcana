package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/KK4ODA/arcana/internal/docs"
	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/storage"
	"github.com/KK4ODA/arcana/internal/tasks"
)

// Limits for one folder import: documents considered, and entries looked
// at (so that choosing a whole drive ends in reasonable time).
const (
	maxFolderFiles   = 100000
	maxFolderEntries = 1000000
)

// systemFolders are skipped wherever they appear: operating-system and
// archive-tool folders that hold no documents of the person's own.
var systemFolders = map[string]bool{
	"$recycle.bin": true, "system volume information": true, "__macosx": true,
	"recycler": true, "lost+found": true, "node_modules": true,
}

// FolderPlan is what adding a folder would do. It is shown before anything
// is copied, and computed again when the copy starts.
type FolderPlan struct {
	Source string `json:"source"`
	// Into is the folder below user/documents that receives the copies.
	Into string `json:"into"`
	// Documents counts the documents found, by extension, before any
	// choice of types: {".pdf": 398, ".txt": 14}.
	Documents map[string]int `json:"documents"`
	// Files and Bytes are the new documents to copy.
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
	// Duplicates are identical to a document already in the library or to
	// another file earlier in the folder; they are not copied again.
	Duplicates     int   `json:"duplicates"`
	DuplicateBytes int64 `json:"duplicateBytes"`
	// Renamed counts the new documents whose names have to change to be
	// valid on every file system.
	Renamed int         `json:"renamed"`
	Skipped []SkipGroup `json:"skipped"`
	// Free is the space left for documents after the copy, above the
	// reserve; Fits is false when the copy would cut into the reserve.
	Free      int64 `json:"free"`
	Reserve   int64 `json:"reserve"`
	Fits      bool  `json:"fits"`
	Truncated bool  `json:"truncated"`
}

// SkipGroup counts files left out for one reason, with a few examples.
type SkipGroup struct {
	Reason   string   `json:"reason"`
	Count    int      `json:"count"`
	Examples []string `json:"examples,omitempty"`
}

// FolderResult is the outcome of adding a folder.
type FolderResult struct {
	Into       string   `json:"into"`
	Copied     int      `json:"copied"`
	Bytes      int64    `json:"bytes"`
	Duplicates int      `json:"duplicates"`
	Renamed    int      `json:"renamed"`
	Skipped    int      `json:"skippedFiles"`
	Failed     []string `json:"failed,omitempty"`
}

type folderItem struct {
	src  string // absolute path in the source folder
	rel  string // slash path below the target folder, with valid names
	ext  string
	size int64
	dup  bool
	// renamed is true when a name had to change to be valid everywhere.
	renamed bool
}

type folderScan struct {
	plan  FolderPlan
	items []folderItem
	skips map[string]*SkipGroup
	order []string
}

func (s *folderScan) skip(reason, example string) {
	g := s.skips[reason]
	if g == nil {
		g = &SkipGroup{Reason: reason}
		s.skips[reason] = g
		s.order = append(s.order, reason)
	}
	g.Count++
	if example != "" && len(g.Examples) < 3 {
		g.Examples = append(g.Examples, example)
	}
}

// PlanFolder looks through a folder on this computer, and its subfolders,
// for documents to add, without copying anything. into names the folder
// below user/documents ("" for the source folder's own name); types limits
// the documents to some extensions (nil for every supported type).
func (a *App) PlanFolder(ctx context.Context, src, into string, types []string) (FolderPlan, error) {
	s, err := a.scanFolder(ctx, src, into, types)
	if err != nil {
		return FolderPlan{}, err
	}
	return s.plan, nil
}

// checkFolder checks that src is a folder on this computer, outside the
// library, and returns it cleaned.
func (a *App) checkFolder(src string) (string, error) {
	if !filepath.IsAbs(src) {
		return "", errors.New("enter the full path of a folder on this computer, such as C:\\Radio\\Manuals or /home/you/Radio")
	}
	root := filepath.Clean(src)
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a folder on this computer", root)
	}
	if inside(root, filepath.Clean(a.Lib.Root)) {
		return "", errors.New("that folder is part of this Arcana library; choose a folder outside it")
	}
	return root, nil
}

func (a *App) scanFolder(ctx context.Context, src, into string, types []string) (*folderScan, error) {
	root, err := a.checkFolder(src)
	if err != nil {
		return nil, err
	}
	libRoot := filepath.Clean(a.Lib.Root)
	if into == "" {
		into = filepath.Base(root)
	}
	if into = fsutil.SanitizeFileName(strings.TrimSpace(into)); into == "" {
		into = "Imported documents"
	}
	want := map[string]bool{}
	for _, t := range types {
		want[strings.ToLower(t)] = true
	}
	s := &folderScan{plan: FolderPlan{Source: root, Into: into, Documents: map[string]int{}}, skips: map[string]*SkipGroup{}}
	unsupported := map[string]int{}
	visited := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if visited++; visited > maxFolderEntries {
			s.plan.Truncated = true
			return fs.SkipAll
		}
		rel, _ := filepath.Rel(root, p)
		if err != nil {
			if p == root {
				return err
			}
			s.skip("could not be read", rel)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		name := d.Name()
		switch {
		case d.IsDir() && samePath(p, libRoot):
			return fs.SkipDir // the library itself, inside the folder
		case strings.HasPrefix(name, ".") || systemFolders[strings.ToLower(name)]:
			s.skip("hidden or system files", "")
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		case d.Type()&fs.ModeSymlink != 0:
			s.skip("links (not followed)", rel)
			return nil
		case d.IsDir():
			if strings.Count(filepath.ToSlash(rel), "/")+2 >= library.MaxDocumentDepth {
				s.skip("folders nested too deeply", rel)
				return fs.SkipDir
			}
			return nil
		case !d.Type().IsRegular():
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		if !docs.Supported(name) {
			unsupported[ext]++
			return nil
		}
		s.plan.Documents[ext]++
		if len(want) > 0 && !want[ext] {
			s.skip("types you left out", "")
			return nil
		}
		info, err := d.Info()
		if err != nil {
			s.skip("could not be read", rel)
			return nil
		}
		switch {
		case info.Size() == 0:
			s.skip("empty files", rel)
			return nil
		case info.Size() > MaxUserDocument:
			s.skip("larger than 2 GB", rel)
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		renamed := false
		for i, part := range parts {
			clean := fsutil.SanitizeFileName(part)
			if clean == "" {
				s.skip("names that cannot be stored", rel)
				return nil
			}
			renamed = renamed || clean != part
			parts[i] = clean
		}
		if len(s.items) >= maxFolderFiles {
			s.plan.Truncated = true
			return fs.SkipAll
		}
		s.items = append(s.items, folderItem{src: p, rel: strings.Join(parts, "/"), ext: ext, size: info.Size(), renamed: renamed})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Files of other types come first, with their commonest extensions as
	// examples: "types Arcana cannot search: 40 .jpg, 12 .docx".
	s.plan.Skipped = []SkipGroup{}
	if len(unsupported) > 0 {
		exts := make([]string, 0, len(unsupported))
		g := SkipGroup{Reason: "types Arcana cannot search"}
		for e, n := range unsupported {
			exts = append(exts, e)
			g.Count += n
		}
		sort.Slice(exts, func(i, j int) bool {
			if unsupported[exts[i]] != unsupported[exts[j]] {
				return unsupported[exts[i]] > unsupported[exts[j]]
			}
			return exts[i] < exts[j]
		})
		for _, e := range exts[:min(len(exts), 5)] {
			label := e
			if label == "" {
				label = "no extension"
			}
			g.Examples = append(g.Examples, fmt.Sprintf("%d %s", unsupported[e], label))
		}
		s.plan.Skipped = append(s.plan.Skipped, g)
	}
	for _, r := range s.order {
		s.plan.Skipped = append(s.plan.Skipped, *s.skips[r])
	}

	// Duplicates: only files whose size matches another file are hashed.
	dd, err := newDedupe(a.Lib)
	if err != nil {
		return nil, err
	}
	sizes := map[int64]int{}
	for _, it := range s.items {
		sizes[it.size]++
	}
	for i := range s.items {
		it := &s.items[i]
		if sizes[it.size] > 1 || dd.hasSize(it.size) {
			sum, err := hashFile(ctx, it.src)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue // unreadable now; the copy will report it
			}
			if it.dup, err = dd.seen(ctx, it.size, sum); err != nil {
				return nil, err
			}
		}
		if it.dup {
			s.plan.Duplicates++
			s.plan.DuplicateBytes += it.size
		} else {
			s.plan.Files++
			s.plan.Bytes += it.size
			if it.renamed {
				s.plan.Renamed++
			}
		}
	}
	// When the free space cannot be read, the copy checks it file by file.
	s.plan.Fits = true
	if vol, err := storage.Stat(a.Lib.Root); err == nil {
		s.plan.Reserve = a.Downloads.Reserve(vol)
		s.plan.Free = int64(vol.AvailBytes) - s.plan.Reserve - s.plan.Bytes
		s.plan.Fits = s.plan.Free >= 0
	}
	return s, nil
}

// StartAddFolder copies the documents of a folder (see PlanFolder) into
// user/documents in the background, keeping its subfolders, then indexes
// them.
func (a *App) StartAddFolder(src, into string, types []string) (tasks.Task, error) {
	if a.Lib.ReadOnly {
		return tasks.Task{}, library.ErrReadOnly
	}
	if _, err := a.checkFolder(src); err != nil {
		return tasks.Task{}, err
	}
	return a.Tasks.Start("documents", "Add folder "+filepath.Base(filepath.Clean(src)), true, func(ctx context.Context, p tasks.Progress) (any, error) {
		res, err := a.AddFolder(ctx, src, into, types, func(done, total int64, msg string) { p(done, total, msg) })
		a.RequestReindex()
		return res, err
	})
}

// AddFolder copies a folder's documents into user/documents/<into>,
// keeping its subfolders. Documents identical to one already in the library
// (or earlier in the folder) are not copied again; a different document
// with the same name is saved as "name (2).ext". Each copy is written to a
// temporary file, synced and then renamed, and the free space is checked
// against the reserve before each file.
func (a *App) AddFolder(ctx context.Context, src, into string, types []string, progress tasks.Progress) (FolderResult, error) {
	if progress == nil {
		progress = func(int64, int64, string) {}
	}
	progress(0, 0, "Looking through the folder")
	s, err := a.scanFolder(ctx, src, into, types)
	if err != nil {
		return FolderResult{}, err
	}
	res := FolderResult{Into: s.plan.Into}
	for _, g := range s.plan.Skipped {
		res.Skipped += g.Count
	}
	if !s.plan.Fits {
		return res, &storage.InsufficientSpaceError{Needed: s.plan.Bytes, Free: max(s.plan.Free+s.plan.Bytes, 0), Reserve: s.plan.Reserve}
	}
	dd, err := newDedupe(a.Lib)
	if err != nil {
		return res, err
	}
	target := filepath.Join(a.Lib.Documents(), s.plan.Into)
	var done int64
	for _, it := range s.items {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		progress(done, s.plan.Bytes, "Copying "+it.rel)
		if vol, err := storage.Stat(a.Lib.Root); err == nil {
			if err := storage.CheckWrite(vol.AvailBytes, it.size, 0, a.Downloads.Reserve(vol)); err != nil {
				return res, err
			}
		}
		copied, dup, err := a.copyDocument(ctx, it, target, dd)
		switch {
		case err != nil && ctx.Err() != nil:
			return res, ctx.Err()
		case err != nil:
			res.Failed = append(res.Failed, it.rel+": "+err.Error())
		case dup:
			res.Duplicates++
		default:
			res.Copied++
			res.Bytes += copied
			done += copied
			if it.renamed {
				res.Renamed++
			}
		}
	}
	progress(done, s.plan.Bytes, fmt.Sprintf("Added %d documents", res.Copied))
	return res, nil
}

// copyDocument copies one file, unless an identical one is already in the
// library. It returns the bytes copied, or dup when nothing was copied.
func (a *App) copyDocument(ctx context.Context, it folderItem, target string, dd *dedupe) (int64, bool, error) {
	// A file the size of a known document is hashed before copying, so an
	// identical one is not copied at all. Any other file is hashed while it
	// is copied, and recorded afterwards: each file is recorded once.
	checked := false
	if dd.hasSize(it.size) {
		sum, err := hashFile(ctx, it.src)
		if err != nil {
			return 0, false, err
		}
		if dup, err := dd.seen(ctx, it.size, sum); err != nil || dup {
			return 0, dup, err
		}
		checked = true
	}
	dst := filepath.Join(target, filepath.FromSlash(it.rel))
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, false, err
	}
	in, err := os.Open(it.src)
	if err != nil {
		return 0, false, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".import-*")
	if err != nil {
		return 0, false, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), &ctxReader{ctx: ctx, r: io.LimitReader(in, MaxUserDocument+1)})
	if err == nil && n != it.size {
		err = errors.New("the file changed while it was being copied")
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return 0, false, err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); !checked {
		if dup, err := dd.seen(ctx, it.size, sum); err != nil || dup {
			os.Remove(tmp.Name())
			return 0, dup, err
		}
	}
	dst = freeName(dst)
	if err := fsutil.MoveFile(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return 0, false, err
	}
	if info, err := os.Stat(it.src); err == nil {
		_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	return n, false, nil
}

// freeName returns p, or "name (2).ext" and so on when p is taken. Lstat
// also finds a name that differs only in case on file systems that ignore
// case, such as exFAT.
func freeName(p string) string {
	dir, base := filepath.Split(p)
	ext := filepath.Ext(base)
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), i, ext))
	}
}

// dedupe recognises documents already in the library, hashing a library
// file only when a new file has the same size.
type dedupe struct {
	bySize map[int64][]string // library documents not yet hashed
	sums   map[string]bool    // hashes of documents known to be present
	sizes  map[int64]bool     // sizes of every known document
}

func newDedupe(lib *library.Library) (*dedupe, error) {
	files, err := lib.DocumentFiles()
	if err != nil {
		return nil, err
	}
	d := &dedupe{bySize: map[int64][]string{}, sums: map[string]bool{}, sizes: map[int64]bool{}}
	for _, f := range files {
		p := filepath.Join(lib.Documents(), filepath.FromSlash(f.Path))
		d.bySize[f.Size] = append(d.bySize[f.Size], p)
		d.sizes[f.Size] = true
	}
	return d, nil
}

func (d *dedupe) hasSize(size int64) bool { return d.sizes[size] }

// seen reports whether a document with this hash is already known, and
// remembers it otherwise.
func (d *dedupe) seen(ctx context.Context, size int64, sum string) (bool, error) {
	for _, p := range d.bySize[size] {
		if s, err := hashFile(ctx, p); err == nil {
			d.sums[s] = true
		} else if ctx.Err() != nil {
			return false, ctx.Err()
		}
	}
	delete(d.bySize, size)
	if d.sums[sum] {
		return true, nil
	}
	d.sums[sum] = true
	d.sizes[size] = true
	return false, nil
}

func hashFile(ctx context.Context, p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ctxReader stops a copy when its context ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// inside reports whether p is dir or inside it.
func inside(p, dir string) bool {
	if foldCase {
		p, dir = strings.ToLower(p), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || foldCase && strings.EqualFold(a, b)
}

// foldCase is true where file names usually ignore case.
var foldCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"
