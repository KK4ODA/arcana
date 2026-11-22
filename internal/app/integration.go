package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/docs"
	"github.com/KK4ODA/arcana/internal/download"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/store"
)

// ---------------------------------------------------------------- events

// Event is a change other programs can follow.
type Event struct {
	Type string    `json:"type"` // "library"
	Time time.Time `json:"time"`
}

type eventHub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// SubscribeEvents returns library change events and an unsubscribe func.
func (a *App) SubscribeEvents() (<-chan Event, func()) {
	ch := make(chan Event, 8)
	a.events.mu.Lock()
	if a.events.subs == nil {
		a.events.subs = map[chan Event]struct{}{}
	}
	a.events.subs[ch] = struct{}{}
	a.events.mu.Unlock()
	return ch, func() {
		a.events.mu.Lock()
		delete(a.events.subs, ch)
		a.events.mu.Unlock()
	}
}

func (a *App) publish(ev Event) {
	ev.Time = time.Now().UTC()
	a.events.mu.Lock()
	defer a.events.mu.Unlock()
	for ch := range a.events.subs {
		select {
		case ch <- ev:
		default: // a slow reader misses events rather than blocking Arcana
		}
	}
}

// ---------------------------------------------------------------- citations

// Citation identifies where text came from. Every content response of the
// integration API carries one, so excerpts travel with their source,
// edition, license and warnings.
type Citation struct {
	ResourceID string            `json:"resourceId"`
	Title      string            `json:"title"`
	Publisher  string            `json:"publisher,omitempty"`
	Edition    string            `json:"edition"`
	Published  string            `json:"published,omitempty"`
	License    citationLicense   `json:"license"`
	SourceURL  string            `json:"sourceUrl,omitempty"`
	Warnings   []catalog.Warning `json:"warnings"`
}

type citationLicense struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	URL         string `json:"url,omitempty"`
	Attribution string `json:"attribution,omitempty"`
}

// CitationFor describes an installed resource.
func CitationFor(in store.Installed) Citation {
	r := in.Resource
	w := r.Warnings
	if w == nil {
		w = []catalog.Warning{}
	}
	return Citation{ResourceID: in.ResourceID, Title: r.Title, Publisher: r.Publisher, Edition: in.Version,
		Published: r.Published, SourceURL: r.SourceURL, Warnings: w,
		License: citationLicense{ID: r.License.ID, Name: r.License.Name, URL: r.License.URL, Attribution: r.License.Attribution}}
}

// Footer is a one-line attribution for plain-text excerpts.
func (c Citation) Footer() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source: %s (%s", c.Title, c.Edition)
	if c.Publisher != "" {
		fmt.Fprintf(&b, ", %s", c.Publisher)
	}
	fmt.Fprintf(&b, "), license %s.", c.License.ID)
	for _, w := range c.Warnings {
		fmt.Fprintf(&b, " Warning: %s", w.Text)
	}
	return b.String()
}

// ---------------------------------------------------------------- articles

// Article is the plain text of a page or document.
type Article struct {
	Title     string `json:"title"`
	Path      string `json:"path"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// ErrBadPath means a path does not belong to the resource.
var ErrBadPath = errors.New("that path does not belong to this resource")

// Truncate shortens text to at most max bytes, at a word boundary.
func Truncate(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if i := strings.LastIndexAny(s[:cut], " \n"); i > max/2 {
		cut = i
	}
	return strings.TrimSpace(s[:cut]) + " …", true
}

// ArticleText returns the text of an installed resource's page (ZIM
// archives: a path as returned by search; documents: optionally the file
// name), with its citation.
func (a *App) ArticleText(ctx context.Context, id, p string, max int) (Article, Citation, error) {
	in, err := a.Store.GetInstalled(ctx, id)
	if err != nil {
		return Article{}, Citation{}, err
	}
	cit := CitationFor(in)
	if in.Resource.Format != "zim" {
		if len(in.Files) == 0 {
			return Article{}, cit, ErrBadPath
		}
		name := path.Base(in.Files[0].Path)
		if p != "" {
			name = path.Base(p)
		}
		title, text, _, err := a.Docs.Text(ctx, "res:"+id+"/"+name, 4<<20)
		if err != nil {
			return Article{}, cit, ErrBadPath
		}
		t, cut := Truncate(text, max)
		return Article{Title: title, Path: name, Text: t, Truncated: cut}, cit, nil
	}
	if a.Kiwix == nil {
		return Article{}, cit, errors.New("the archive reader is not available")
	}
	book, ok := a.Kiwix.Books()[id]
	if !ok {
		return Article{}, cit, errors.New("this archive is not open in the reader")
	}
	if p == "" {
		p = book.ContentPath
	}
	for hop := 0; ; hop++ {
		u, err := url.Parse(p)
		if err != nil || !strings.HasPrefix(u.Path, book.ContentPath) || strings.Contains(u.Path, "..") {
			return Article{}, cit, ErrBadPath
		}
		req := httptest.NewRequest(http.MethodGet, u.RequestURI(), nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		a.Kiwix.Handler().ServeHTTP(rec, req)
		switch {
		case rec.Code >= 300 && rec.Code < 400 && hop < 3:
			loc, err := u.Parse(rec.Header().Get("Location"))
			if err != nil {
				return Article{}, cit, ErrBadPath
			}
			p = loc.RequestURI()
			continue
		case rec.Code != http.StatusOK:
			return Article{}, cit, fmt.Errorf("page not found (%d)", rec.Code)
		}
		ct := rec.Header().Get("Content-Type")
		var title, text string
		switch {
		case strings.HasPrefix(ct, "text/html"):
			title, text, _ = docs.HTMLText(io.LimitReader(rec.Body, 8<<20))
		case strings.HasPrefix(ct, "text/plain"):
			b, _ := io.ReadAll(io.LimitReader(rec.Body, 4<<20))
			text = string(b)
		default:
			return Article{}, cit, fmt.Errorf("this page is %s, not text", ct)
		}
		t, cut := Truncate(text, max)
		return Article{Title: title, Path: u.RequestURI(), Text: t, Truncated: cut}, cit, nil
	}
}

// ---------------------------------------------------------------- peers

// PeerFile is one file of a resource offered by another Arcana.
type PeerFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// PeerResource is an installed resource as another Arcana reports it.
type PeerResource struct {
	Resource     catalog.Resource `json:"resource"`
	Version      string           `json:"version"`
	Files        []PeerFile       `json:"files"`
	Verification string           `json:"verification"`
	Unverified   bool             `json:"unverified"`
	// Filled in by the receiving library:
	LocalVersion string `json:"localVersion,omitempty"`
	Status       string `json:"status,omitempty"` // new, update, installed, older
}

// PeerLibraryResponse is what /api/integration/v1/library returns.
type PeerLibraryResponse struct {
	Library   map[string]string `json:"library"`
	Resources []PeerResource    `json:"resources"`
}

// LibraryForPeers lists installed resources for another Arcana.
func (a *App) LibraryForPeers(ctx context.Context) (PeerLibraryResponse, error) {
	list, err := a.Store.ListInstalled(ctx)
	if err != nil {
		return PeerLibraryResponse{}, err
	}
	out := PeerLibraryResponse{Library: map[string]string{"id": a.Lib.Identity.LibraryID, "label": a.Lib.Identity.Label}, Resources: []PeerResource{}}
	for _, in := range list {
		pr := PeerResource{Resource: in.Resource, Version: in.Version, Verification: in.Verification, Unverified: in.Unverified}
		for _, f := range in.Files {
			pr.Files = append(pr.Files, PeerFile{Name: path.Base(f.Path), Size: f.Size, SHA256: f.SHA256})
		}
		out.Resources = append(out.Resources, pr)
	}
	return out, nil
}

func (a *App) peer(id string) (library.PeerSource, error) {
	for _, p := range a.Lib.Settings.Get().Peers {
		if p.ID == id {
			return p, nil
		}
	}
	return library.PeerSource{}, errors.New("unknown peer")
}

var peerDNS = struct {
	sync.Mutex
	at    time.Time
	addrs map[string]bool
}{}

// peerAddrAllowed lets downloads reach configured peers on the local
// network even though catalog downloads may not.
func (a *App) peerAddrAllowed(address string) bool {
	peers := a.Lib.Settings.Get().Peers
	if len(peers) == 0 {
		return false
	}
	peerDNS.Lock()
	defer peerDNS.Unlock()
	if time.Since(peerDNS.at) > time.Minute || peerDNS.addrs == nil {
		m := map[string]bool{}
		for _, p := range peers {
			u, err := url.Parse(p.URL)
			if err != nil {
				continue
			}
			port := u.Port()
			if port == "" {
				port = map[bool]string{true: "443", false: "80"}[u.Scheme == "https"]
			}
			m[net.JoinHostPort(u.Hostname(), port)] = true
			if ips, err := net.LookupHost(u.Hostname()); err == nil {
				for _, ip := range ips {
					m[net.JoinHostPort(ip, port)] = true
				}
			}
		}
		peerDNS.addrs, peerDNS.at = m, time.Now()
	}
	return peerDNS.addrs[address]
}

func (a *App) peerHTTP() *http.Client {
	c := download.NewClientFor(func() bool { return a.Lib.Settings.Get().AllowPrivateNetworkDownloads }, a.peerAddrAllowed)
	c.Timeout = 60 * time.Second
	return c
}

// peerRequestHook adds a peer's token to download requests for its files.
func (a *App) peerRequestHook(req *http.Request) {
	for _, p := range a.Lib.Settings.Get().Peers {
		if strings.HasPrefix(req.URL.String(), strings.TrimRight(p.URL, "/")+"/api/integration/v1/") {
			req.Header.Set("Authorization", "Bearer "+p.Token)
			return
		}
	}
}

// PeerLibrary fetches another Arcana's installed resources and compares
// them with this library.
func (a *App) PeerLibrary(ctx context.Context, peerID string) (PeerLibraryResponse, error) {
	p, err := a.peer(peerID)
	if err != nil {
		return PeerLibraryResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.URL, "/")+"/api/integration/v1/library", nil)
	if err != nil {
		return PeerLibraryResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("User-Agent", catalog.UserAgent())
	resp, err := a.peerHTTP().Do(req)
	if err != nil {
		return PeerLibraryResponse{}, fmt.Errorf("cannot reach %s: %w", p.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return PeerLibraryResponse{}, fmt.Errorf("%s answered %s (check its access token and that it allows library copies)", p.Name, resp.Status)
	}
	var out PeerLibraryResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&out); err != nil {
		return out, fmt.Errorf("%s sent an unreadable list: %w", p.Name, err)
	}
	local := map[string]string{}
	if list, err := a.Store.ListInstalled(ctx); err == nil {
		for _, in := range list {
			local[in.ResourceID] = in.Version
		}
	}
	valid := out.Resources[:0]
	for _, r := range out.Resources {
		// The list is untrusted input like any catalog.
		if r.Resource.Version != r.Version || r.Resource.Validate() != nil || len(r.Files) == 0 {
			continue
		}
		r.LocalVersion = local[r.Resource.ID]
		switch c := catalog.CompareVersions(r.Version, r.LocalVersion); {
		case r.LocalVersion == "":
			r.Status = "new"
		case c > 0:
			r.Status = "update"
		case c == 0:
			r.Status = "installed"
		default:
			r.Status = "older"
		}
		valid = append(valid, r)
	}
	out.Resources = valid
	return out, nil
}

// PullFromPeer queues copies of resources from another Arcana. Files are
// verified against the SHA-256 the peer recorded, and against this
// library's catalog when it publishes a checksum for the same edition.
func (a *App) PullFromPeer(ctx context.Context, peerID string, ids []string) (map[string]string, error) {
	if a.Lib.ReadOnly {
		return nil, library.ErrReadOnly
	}
	p, err := a.peer(peerID)
	if err != nil {
		return nil, err
	}
	lib, err := a.PeerLibrary(ctx, peerID)
	if err != nil {
		return nil, err
	}
	byID := map[string]PeerResource{}
	for _, r := range lib.Resources {
		byID[r.Resource.ID] = r
	}
	base := strings.TrimRight(p.URL, "/")
	results := map[string]string{}
	for _, id := range ids {
		pr, ok := byID[id]
		if !ok {
			results[id] = "not offered by " + p.Name
			continue
		}
		res, err := a.peerResource(ctx, pr, base, p)
		if err == nil {
			_, err = a.Downloads.Enqueue(ctx, res)
		}
		if err != nil {
			results[id] = err.Error()
			continue
		}
		results[id] = "queued"
	}
	return results, nil
}

func (a *App) peerResource(ctx context.Context, pr PeerResource, base string, p library.PeerSource) (catalog.Resource, error) {
	res := pr.Resource
	published := map[string]string{}
	if cat, err := a.Store.GetResource(ctx, res.ID); err == nil && cat.Version == res.Version {
		for _, f := range cat.Files {
			if h := f.Hashes["sha256"]; h != "" {
				published[f.Name] = strings.ToLower(h)
			}
		}
	}
	var files []catalog.File
	var total int64
	for _, f := range pr.Files {
		if len(f.SHA256) != 64 || f.Size <= 0 {
			return res, fmt.Errorf("%s gave no checksum for %s", p.Name, f.Name)
		}
		if h, ok := published[f.Name]; ok && h != strings.ToLower(f.SHA256) {
			return res, fmt.Errorf("%s's copy of %s does not match the publisher's checksum", p.Name, f.Name)
		}
		u := base + "/api/integration/v1/files/" + url.PathEscape(res.ID) + "/" + url.PathEscape(f.Name)
		files = append(files, catalog.File{Name: f.Name, Size: f.Size, Hashes: map[string]string{"sha256": strings.ToLower(f.SHA256)},
			Mirrors: []catalog.Mirror{{URL: u}}})
		total += f.Size
	}
	res.Files, res.DownloadSize, res.SizeEstimated = files, total, false
	ref := map[string]string{}
	for k, v := range res.ProviderRef {
		ref[k] = v
	}
	delete(ref, "method") // copy the finished file, never extract again
	ref["via"], ref["peer"], ref["peerName"] = "peer", p.ID, p.Name
	if pr.Unverified {
		ref["peerUnverified"] = "true"
	}
	res.ProviderRef = ref
	return res, res.Validate()
}
