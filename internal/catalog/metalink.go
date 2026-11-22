package catalog

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// MetalinkFile is one <file> from a Metalink 4 (RFC 5854) document.
type MetalinkFile struct {
	Name    string
	Size    int64
	Hashes  map[string]string
	Mirrors []Mirror
}

type metalinkDoc struct {
	XMLName xml.Name `xml:"metalink"`
	Files   []struct {
		Name   string `xml:"name,attr"`
		Size   int64  `xml:"size"`
		Hashes []struct {
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"hash"`
		URLs []struct {
			Location string `xml:"location,attr"`
			Priority int    `xml:"priority,attr"`
			Value    string `xml:",chardata"`
		} `xml:"url"`
	} `xml:"file"`
}

// metalinkHashNames maps RFC 5854 / IANA hash names onto Arcana's.
var metalinkHashNames = map[string]string{
	"sha-256": "sha256", "sha-512": "sha512", "sha-1": "sha1", "md5": "md5",
}

// ParseMetalink parses a Metalink 4 document. Go's XML decoder does not
// resolve external entities, so untrusted input cannot read local files.
func ParseMetalink(data []byte) ([]MetalinkFile, error) {
	var doc metalinkDoc
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("metalink: %w", err)
	}
	var out []MetalinkFile
	for _, f := range doc.Files {
		mf := MetalinkFile{Name: strings.TrimSpace(f.Name), Size: f.Size, Hashes: map[string]string{}}
		for _, h := range f.Hashes {
			if name, ok := metalinkHashNames[strings.ToLower(h.Type)]; ok {
				mf.Hashes[name] = strings.ToLower(strings.TrimSpace(h.Value))
			}
		}
		for _, u := range f.URLs {
			v := strings.TrimSpace(u.Value)
			if !isHTTPURL(v) {
				continue
			}
			p := u.Priority
			if p <= 0 {
				p = 999999
			}
			mf.Mirrors = append(mf.Mirrors, Mirror{URL: v, Priority: p, Location: u.Location})
		}
		sort.SliceStable(mf.Mirrors, func(i, j int) bool { return mf.Mirrors[i].Priority < mf.Mirrors[j].Priority })
		out = append(out, mf)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("metalink: no files")
	}
	return out, nil
}
