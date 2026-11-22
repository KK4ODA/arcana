// Command release-sign creates and signs Arcana release manifests.
//
//	release-sign keygen -out arcana-release.key          # prints the public key
//	release-sign manifest -dir dist/Arcana -version 0.2.0 [-archive dist/arcana-0.2.0-portable.zip]
//	release-sign sign -key arcana-release.key -in dist/Arcana/release.json
//
// Keep the private key offline. Build releases with
//
//	-ldflags "-X github.com/KK4ODA/arcana/internal/buildinfo.UpdateKeys=<public key>"
//
// so that Arcana can verify them.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/appupdate"
	"github.com/KK4ODA/arcana/internal/library"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: release-sign keygen|manifest|sign ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "manifest":
		err = manifest(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release-sign:", err)
		os.Exit(1)
	}
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "arcana-release.key", "private key file to create")
	fs.Parse(args)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s exists; refusing to overwrite a signing key", *out)
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

func sha(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func manifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	dir := fs.String("dir", "dist/Arcana", "unpacked release folder")
	version := fs.String("version", "", "release version")
	notes := fs.String("notes", "", "release notes")
	archive := fs.String("archive", "", "release zip (enables online updates)")
	fs.Parse(args)
	if *version == "" {
		return fmt.Errorf("-version is required")
	}
	m := appupdate.Manifest{Format: "arcana-release", FormatVersion: 1, Version: strings.TrimPrefix(*version, "v"),
		Released: time.Now().UTC().Format(time.RFC3339), Notes: *notes,
		MinLibraryFormat: 1, MaxLibraryFormat: library.FormatVersion, ArchiveRoot: filepath.Base(*dir)}
	err := filepath.Walk(*dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(*dir, p)
		rel = filepath.ToSlash(rel)
		if rel == appupdate.ManifestFile || rel == appupdate.SignatureFile || strings.HasPrefix(rel, "content/") {
			return nil
		}
		sum, size, err := sha(p)
		if err != nil {
			return err
		}
		exec := rel == "Arcana.exe" || rel == "arcana.sh" || strings.HasSuffix(rel, "/arcana") ||
			strings.HasSuffix(rel, "/kiwix-serve") || strings.HasSuffix(rel, ".exe")
		m.Files = append(m.Files, appupdate.File{Path: rel, Size: size, SHA256: sum, Executable: exec})
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	if *archive != "" {
		sum, size, err := sha(*archive)
		if err != nil {
			return err
		}
		m.Archive = &appupdate.File{Path: filepath.Base(*archive), Size: size, SHA256: sum}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	out := filepath.Join(*dir, appupdate.ManifestFile)
	if *archive != "" {
		out = filepath.Join(filepath.Dir(*archive), appupdate.ManifestFile)
	}
	fmt.Println(out)
	return os.WriteFile(out, append(data, '\n'), 0o644)
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	keyFile := fs.String("key", "", "private key file (or set ARCANA_SIGNING_KEY)")
	in := fs.String("in", "", "release.json to sign")
	fs.Parse(args)
	raw := os.Getenv("ARCANA_SIGNING_KEY")
	if *keyFile != "" {
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid private key")
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	return os.WriteFile(*in+".sig", appupdate.Sign(data, ed25519.PrivateKey(key)), 0o644)
}
