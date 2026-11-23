package app

import (
	"context"
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/KK4ODA/arcana/internal/translate"
)

// TranslateProvider returns the translation model provider, or nil when it
// is disabled.
func (a *App) TranslateProvider() *translate.Provider {
	if !a.Lib.Settings.Get().Catalog.TranslationsEnabled {
		return nil
	}
	return &translate.Provider{}
}

// TranslationLanguage is an installed language pack.
type TranslationLanguage struct {
	Code       string                `json:"code"`
	Name       string                `json:"name"`
	ResourceID string                `json:"resourceId"`
	Version    string                `json:"version"`
	Directions []string              `json:"directions"`
	Files      []translate.ModelFile `json:"files"`
}

// TranslationEngine is the installed engine.
type TranslationEngine struct {
	Path             string
	Version          string
	DecompressedHash string
	DecompressedSize int64
}

// Translation lists the installed engine (nil if none) and language packs.
func (a *App) Translation(ctx context.Context) (*TranslationEngine, []TranslationLanguage) {
	installed, err := a.Store.ListInstalled(ctx)
	if err != nil {
		return nil, nil
	}
	var eng *TranslationEngine
	var langs []TranslationLanguage
	for _, in := range installed {
		switch in.Resource.Format {
		case translate.EngineFormat:
			if len(in.Files) != 1 {
				continue
			}
			p, err := a.Content.FilePath(in, in.Files[0])
			if err != nil {
				continue
			}
			size, _ := strconv.ParseInt(in.Resource.ProviderRef["decompressedSize"], 10, 64)
			eng = &TranslationEngine{Path: p, Version: in.Version, DecompressedHash: in.Resource.ProviderRef["decompressedHash"], DecompressedSize: size}
		case translate.Format:
			files, err := translate.Manifest(in.Resource)
			if err != nil {
				continue
			}
			code := strings.TrimPrefix(in.ResourceID, "translations.")
			langs = append(langs, TranslationLanguage{Code: code, Name: translate.LanguageName(code), ResourceID: in.ResourceID,
				Version: in.Version, Directions: strings.Split(in.Resource.ProviderRef["directions"], ","), Files: files})
		}
	}
	sort.Slice(langs, func(i, j int) bool { return langs[i].Name < langs[j].Name })
	return eng, langs
}

// ErrNoSuchFile means the file is not part of an installed language pack.
var ErrNoSuchFile = errors.New("no such translation file")

// TranslationFile locates one installed model file.
func (a *App) TranslationFile(ctx context.Context, resourceID, name string) (string, translate.ModelFile, error) {
	in, err := a.Store.GetInstalled(ctx, resourceID)
	if err != nil || in.Resource.Format != translate.Format {
		return "", translate.ModelFile{}, ErrNoSuchFile
	}
	files, err := translate.Manifest(in.Resource)
	if err != nil {
		return "", translate.ModelFile{}, err
	}
	for _, mf := range files {
		if mf.Name != name {
			continue
		}
		for _, f := range in.Files {
			if path.Base(f.Path) == name {
				p, err := a.Content.FilePath(in, f)
				return p, mf, err
			}
		}
	}
	return "", translate.ModelFile{}, ErrNoSuchFile
}
