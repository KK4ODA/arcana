package kolibri

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // SQLite driver
)

// StudioURL is Kolibri Studio, where Kolibri imports channels from.
const StudioURL = "https://studio.learningequality.org"

// Channel is a Kolibri channel Arcana offers (the list is Channels).
type Channel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Language is the channel's language as Kolibri Studio gives it
	// (BCP 47: "en", "pt-BR", "fuv").
	Language string `json:"language"`
	// Group places the channel on the Classroom page.
	Group string `json:"group"`
	// Levels says who the channel is for and About what it holds, so the
	// owner can choose without opening its list of topics.
	Levels string `json:"levels"`
	About  string `json:"about"`
	// Size is the whole channel as last published.
	Size      int64  `json:"size"`
	Published string `json:"published"`
	// License is how the channel marks its resources. Kolibri shows each
	// resource's own licence.
	License string `json:"license"`
}

// The Classroom page's groups of channels.
const (
	// GroupKhan: Khan Academy in English (US, Philippines and India),
	// Spanish, and its archive of test preparation.
	GroupKhan = "khan"
	// GroupMore: other publishers' channels for high school, college and
	// adults, in English and Spanish.
	GroupMore = "more"
	// GroupKhanWorld: Khan Academy in other languages.
	GroupKhanWorld = "khan-world"
)

// ChannelByID returns an offered channel.
func ChannelByID(id string) (Channel, bool) {
	for _, c := range Channels {
		if c.ID == id {
			return c, true
		}
	}
	return Channel{}, false
}

// Node is a topic of a channel, with the size of everything under it.
// Resources counts distinct videos, exercises and readings: Khan Academy
// places many of them under several topics.
type Node struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Resources int    `json:"resources"`
	Children  []Node `json:"children,omitempty"`
}

// ChannelDB is a channel's database as Kolibri Studio publishes it.
type ChannelDB struct {
	db     *sql.DB
	ID     string
	Name   string
	Ver    int
	rootID string
}

// OpenChannelDB opens a downloaded channel database read-only.
func OpenChannelDB(path string) (*ChannelDB, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	c := &ChannelDB{db: db}
	err = db.QueryRow(`SELECT id, name, version, root_id FROM content_channelmetadata LIMIT 1`).Scan(&c.ID, &c.Name, &c.Ver, &c.rootID)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("not a Kolibri channel database: %w", err)
	}
	return c, nil
}

// Close closes the database.
func (c *ChannelDB) Close() error { return c.db.Close() }

type span struct {
	id, title, kind string
	tree, lft, rght int64
}

func (c *ChannelDB) children(ctx context.Context, parent string) ([]span, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id, title, kind, tree_id, lft, rght FROM content_contentnode
		WHERE parent_id = ? ORDER BY lft`, parent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []span
	for rows.Next() {
		var s span
		if err := rows.Scan(&s.id, &s.title, &s.kind, &s.tree, &s.lft, &s.rght); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// measure counts the bytes (each file once) and resources under spans.
func (c *ChannelDB) measure(ctx context.Context, spans []span) (int64, int, error) {
	if len(spans) == 0 {
		return 0, 0, nil
	}
	var cond []string
	var args []any
	for _, s := range spans {
		cond = append(cond, "(n.tree_id = ? AND n.lft BETWEEN ? AND ?)")
		args = append(args, s.tree, s.lft, s.rght)
	}
	where := strings.Join(cond, " OR ")
	var size int64
	err := c.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(lf.file_size), 0) FROM content_localfile lf
		WHERE lf.id IN (SELECT f.local_file_id FROM content_file f
			JOIN content_contentnode n ON n.id = f.contentnode_id WHERE `+where+`)`, args...).Scan(&size)
	if err != nil {
		return 0, 0, err
	}
	var count int
	err = c.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT n.content_id) FROM content_contentnode n
		WHERE n.kind != 'topic' AND (`+where+`)`, args...).Scan(&count)
	return size, count, err
}

// Tree returns the channel's topics to depth levels below the root, each
// with its size. The root itself is the returned node.
func (c *ChannelDB) Tree(ctx context.Context, depth int) (Node, error) {
	var root span
	err := c.db.QueryRowContext(ctx, `SELECT id, title, kind, tree_id, lft, rght FROM content_contentnode WHERE id = ?`, c.rootID).
		Scan(&root.id, &root.title, &root.kind, &root.tree, &root.lft, &root.rght)
	if err != nil {
		return Node{}, err
	}
	return c.node(ctx, root, depth)
}

func (c *ChannelDB) node(ctx context.Context, s span, depth int) (Node, error) {
	size, count, err := c.measure(ctx, []span{s})
	if err != nil {
		return Node{}, err
	}
	n := Node{ID: s.id, Title: s.title, Kind: s.kind, Size: size, Resources: count}
	if depth <= 0 || s.kind != "topic" {
		return n, nil
	}
	kids, err := c.children(ctx, s.id)
	if err != nil {
		return Node{}, err
	}
	for _, k := range kids {
		if k.kind != "topic" {
			continue // single resources at this level come with their parent
		}
		child, err := c.node(ctx, k, depth-1)
		if err != nil {
			return Node{}, err
		}
		if child.Resources > 0 {
			n.Children = append(n.Children, child)
		}
	}
	return n, nil
}

// SizeOf measures a selection of topics, counting files shared between
// them once.
func (c *ChannelDB) SizeOf(ctx context.Context, nodeIDs []string) (int64, int, error) {
	var spans []span
	seen := map[string]bool{}
	for _, id := range nodeIDs {
		if !IsHexID(id) || seen[id] {
			continue
		}
		seen[id] = true
		var s span
		err := c.db.QueryRowContext(ctx, `SELECT id, title, kind, tree_id, lft, rght FROM content_contentnode WHERE id = ?`, id).
			Scan(&s.id, &s.title, &s.kind, &s.tree, &s.lft, &s.rght)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, fmt.Errorf("topic %s is not in this channel", id)
		}
		if err != nil {
			return 0, 0, err
		}
		spans = append(spans, s)
	}
	return c.measure(ctx, spans)
}

// Has reports whether every ID is a node of the channel.
func (c *ChannelDB) Has(ctx context.Context, nodeIDs []string) error {
	for _, id := range nodeIDs {
		if !IsHexID(id) {
			return errors.New("not a topic ID")
		}
		var one int
		if err := c.db.QueryRowContext(ctx, `SELECT 1 FROM content_contentnode WHERE id = ?`, id).Scan(&one); err != nil {
			return fmt.Errorf("topic %s is not in this channel", id)
		}
	}
	return nil
}

// FetchChannelDB downloads a channel's database from Kolibri Studio into
// dest, checking that it opens and is the channel asked for.
func FetchChannelDB(ctx context.Context, client *http.Client, studio, id, dest string) error {
	if !IsHexID(id) {
		return errors.New("not a channel ID")
	}
	if studio == "" {
		studio = StudioURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(studio, "/")+"/content/databases/"+id+".sqlite3", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Kolibri Studio: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Kolibri Studio answered %s for the channel's contents", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".channel-*.sqlite3")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	// Channel databases are tens of megabytes; 2 GB is far beyond any.
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, 2<<30+1))
	if err == nil && n > 2<<30 {
		err = errors.New("the channel's contents are implausibly large")
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	c, err := OpenChannelDB(tmp.Name())
	if err != nil {
		return err
	}
	got := c.ID
	c.Close()
	if got != id {
		return fmt.Errorf("Kolibri Studio sent channel %s instead of %s", got, id)
	}
	return os.Rename(tmp.Name(), dest)
}

// OnDevice is a channel Kolibri has, with what of it is on this device.
type OnDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   int    `json:"version"`
	Resources int    `json:"resources"`
	Size      int64  `json:"size"`
}

// Channels lists the channels on the device with what of each is
// imported (as the administrator). The device plugin serves its API under
// its own prefix, /device/api/, not /api/device/ (Kolibri 0.19.5).
func (a *Admin) Channels(ctx context.Context) ([]OnDevice, error) {
	var raw []struct {
		ID        string      `json:"id"`
		Name      string      `json:"name"`
		Version   int         `json:"version"`
		Resources json.Number `json:"on_device_resources"`
		Size      json.Number `json:"on_device_file_size"`
	}
	if err := a.call(ctx, http.MethodGet, "/device/api/device_channel/?include_fields=on_device_resources,on_device_file_size", nil, &raw); err != nil {
		return nil, err
	}
	out := []OnDevice{}
	for _, r := range raw {
		res, _ := r.Resources.Int64()
		size, _ := r.Size.Int64()
		out = append(out, OnDevice{ID: r.ID, Name: r.Name, Version: r.Version, Resources: int(res), Size: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// channelDBAge is how long a downloaded channel database is used before
// it is fetched again to show newer sizes.
const channelDBAge = 30 * 24 * time.Hour

// StaleChannelDB reports whether the channel database at p should be
// fetched again.
func StaleChannelDB(p string) bool {
	st, err := os.Stat(p)
	return err != nil || time.Since(st.ModTime()) > channelDBAge
}
