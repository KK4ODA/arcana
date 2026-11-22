package download

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/store"
)

// ExtractRequest asks an Extractor to produce a resource's single file.
type ExtractRequest struct {
	Resource catalog.Resource
	// Out is the staging path to write; the extractor may keep checkpoint
	// files next to it (Out + suffix) and must resume from them.
	Out    string
	Client *http.Client
	// Planned reports the exact output size once known. It returns an
	// error when the drive cannot hold it.
	Planned func(size int64) error
	// Progress reports output bytes written so far.
	Progress func(done int64)
}

// Extractor installs a resource by a method other than downloading its
// files, such as cutting a map region out of a remote archive. It returns
// the output size and SHA-256.
type Extractor func(ctx context.Context, req ExtractRequest) (size int64, sha256 string, err error)

// ErrRestart tells the manager that partial work is no longer usable (the
// source changed or disappeared) and the job must be resolved again.
var ErrRestart = errors.New("source changed; restarting")

func (m *Manager) extractor(res catalog.Resource) (Extractor, string) {
	method := res.ProviderRef["method"]
	if method == "" {
		return nil, ""
	}
	return m.opts.Extractors[method], method
}

// extractFile runs an extractor for a single-file resource.
func (m *Manager) extractFile(ctx context.Context, j *store.Job, rs *runState, ex Extractor, persist func()) (store.InstalledFile, error) {
	if len(j.Resource.Files) != 1 {
		return store.InstalledFile{}, fail(kindFatal, "extracted resources must have exactly one file")
	}
	f := j.Resource.Files[0]
	if err := os.MkdirAll(m.stagingDir(j.ID), 0o755); err != nil {
		return store.InstalledFile{}, fail(kindFatal, "create staging directory: %v", err)
	}
	rs.mirror.Store(hostOf(f.Mirrors[0].URL))
	var (
		last           int64 = j.Offset
		lastSample           = time.Now()
		lastSampleDone       = j.Offset
		lastPersist          = time.Now()
		throttleErr    error
	)
	req := ExtractRequest{
		Resource: j.Resource,
		Out:      m.partPath(j.ID, f.Name),
		Client:   m.opts.Client,
		Planned: func(size int64) error {
			if j.Resource.DownloadSize != size || j.Resource.SizeEstimated {
				j.Resource.DownloadSize = size
				j.Resource.SizeEstimated = false
				persist()
			}
			return m.checkSpace(ctx, j, size)
		},
		Progress: func(done int64) {
			if done > last && throttleErr == nil {
				throttleErr = m.throttle(ctx, int(done-last))
			}
			last = done
			j.Offset = done
			rs.bytesDone.Store(done)
			if now := time.Now(); now.Sub(lastSample) >= time.Second {
				inst := float64(done-lastSampleDone) / now.Sub(lastSample).Seconds()
				prev := float64(rs.speed.Load())
				if prev == 0 {
					prev = inst
				}
				rs.speed.Store(int64(0.7*prev + 0.3*inst))
				lastSample, lastSampleDone = now, done
			}
			if time.Since(lastPersist) >= m.opts.CheckpointInterval {
				persist()
				lastPersist = time.Now()
			}
		},
	}
	size, sum, err := ex(ctx, req)
	if err != nil {
		var je *jobError
		switch {
		case errors.As(err, &je):
			return store.InstalledFile{}, err
		case ctx.Err() != nil:
			return store.InstalledFile{}, ctx.Err()
		case errors.Is(err, ErrRestart):
			// Start over from resolution so the newest source is used.
			m.removeStaging(j.ID)
			j.Resolved = false
			j.Offset = 0
			return store.InstalledFile{}, &jobError{kind: kindServer, err: err}
		case isNoSpace(err):
			return store.InstalledFile{}, &jobError{kind: kindSpace, err: errors.New("the drive is full")}
		}
		return store.InstalledFile{}, classifyNetErr(err, "extract "+f.Name)
	}
	return store.InstalledFile{Path: path.Join(j.Version, f.Name), Size: size, SHA256: sum}, nil
}
