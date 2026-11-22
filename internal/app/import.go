package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/storage"
)

// ImportOptions control ImportFile.
type ImportOptions struct {
	// ResourceID selects the catalog entry; by default it is found by file
	// name.
	ResourceID string
	// AllowUnverified accepts a file whose catalog entry has no checksum
	// available offline. The installation is then marked unverified.
	AllowUnverified bool
	// Resolve permits fetching a metalink to learn the checksum (requires
	// network access).
	Resolve bool
}

// ErrNeedsChecksum is returned when an import cannot be verified offline.
var ErrNeedsChecksum = errors.New("the catalog has no checksum for this file available offline; connect to the internet so Arcana can fetch it, or import with --allow-unverified")

// ImportFile installs a file already on disk (for example from another
// drive) as a catalog resource, without downloading it. The file is copied,
// never moved, and is verified against the catalog's checksum before it is
// published.
func (a *App) ImportFile(ctx context.Context, src string, opt ImportOptions) (catalog.Resource, error) {
	if a.Lib.ReadOnly {
		return catalog.Resource{}, library.ErrReadOnly
	}
	name := filepath.Base(src)
	var res catalog.Resource
	if opt.ResourceID != "" {
		r, err := a.Store.GetResource(ctx, opt.ResourceID)
		if err != nil {
			return res, fmt.Errorf("%s: %w", opt.ResourceID, err)
		}
		res = r
	} else {
		matches, err := a.Store.FindByFileName(ctx, name)
		if err != nil {
			return res, err
		}
		switch len(matches) {
		case 0:
			return res, fmt.Errorf("%s does not match any catalog entry; refresh the catalog or pass --id", name)
		case 1:
			res = matches[0]
		default:
			return res, fmt.Errorf("%s matches %d catalog entries; pass --id", name, len(matches))
		}
	}
	if len(res.Files) != 1 {
		return res, errors.New("only single-file resources can be imported in this version")
	}
	if opt.Resolve {
		if r, ok := a.resolvers()[res.Provider]; ok {
			if resolved, err := r.Resolve(ctx, a.Fetcher, res); err == nil {
				res = resolved
			}
		}
	}
	file := res.Files[0]
	st, err := os.Stat(src)
	if err != nil {
		return res, err
	}
	if file.Size > 0 && st.Size() != file.Size {
		return res, fmt.Errorf("%s is %d bytes; the catalog edition %s is %d bytes", name, st.Size(), res.Version, file.Size)
	}
	algo := ""
	for _, alg := range []string{"sha256"} {
		if file.Hashes[alg] != "" {
			algo = alg
		}
	}
	if algo == "" && !opt.AllowUnverified {
		return res, ErrNeedsChecksum
	}
	vol, err := storage.Stat(a.Lib.Root)
	if err != nil {
		return res, err
	}
	if err := storage.CheckWrite(vol.AvailBytes, st.Size(), 0, a.Downloads.Reserve(vol)); err != nil {
		return res, err
	}

	stagingDir := filepath.Join(a.Lib.Staging(), "import-"+res.ID)
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return res, err
	}
	defer os.RemoveAll(stagingDir)
	staged := filepath.Join(stagingDir, file.Name+".part")
	sum, err := copyAndHash(ctx, src, staged)
	if err != nil {
		return res, err
	}
	verification := "sha256 matched the value published by " + res.Provider + " (imported from another drive)"
	unverified := false
	if algo == "" {
		verification = "imported without a published checksum; SHA-256 recorded for future checks"
		unverified = true
	} else if sum != file.Hashes["sha256"] {
		return res, fmt.Errorf("%s failed verification: the file is damaged or is a different edition", name)
	}
	_, err = a.Content.Publish(ctx, res, []content.StagedFile{{Path: staged, Name: file.Name, Size: st.Size(), SHA256: sum}}, verification, unverified)
	return res, err
}

func copyAndHash(ctx context.Context, src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			out.Close()
			return "", err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return "", err
			}
			h.Write(buf[:n])
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return "", rerr
		}
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
