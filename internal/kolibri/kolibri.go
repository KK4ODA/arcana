// Package kolibri connects Arcana to a Kolibri server (Learning Equality's
// offline learning platform, which hosts Khan Academy and other courses).
// Kolibri is a separate program; Arcana only searches it and links to it,
// so courses show up next to the library's own results.
package kolibri

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Item is one search result.
type Item struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
	ChannelID   string `json:"channelId,omitempty"`
	// URL opens the item in Kolibri's Learn page.
	URL string `json:"url"`
}

// Info describes the server.
type Info struct {
	Version    string `json:"version,omitempty"`
	DeviceName string `json:"deviceName,omitempty"`
}

// Client talks to one Kolibri server.
type Client struct {
	BaseURL string
	// PublicURL, when set, is the address links use (the built-in
	// Kolibri's origin as the browser sees it); BaseURL otherwise.
	PublicURL string
	HTTP      *http.Client
}

// ErrSignIn means the server does not let guests browse content.
var ErrSignIn = errors.New("this Kolibri server asks visitors to sign in; turn on guest access in its facility settings, or open Kolibri directly")

// Normalize validates a Kolibri address (http or https, no query).
func Normalize(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("enter Kolibri's address, for example http://192.168.1.20:8080")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 8 * time.Second}
}

func (c *Client) get(ctx context.Context, p string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+p, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Kolibri: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrSignIn
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("Kolibri answered %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

// Info reads the server's public description (/api/public/info/).
func (c *Client) Info(ctx context.Context) (Info, error) {
	var raw struct {
		Version    string `json:"kolibri_version"`
		DeviceName string `json:"device_name"`
	}
	if err := c.get(ctx, "/api/public/info/", &raw); err != nil {
		return Info{}, err
	}
	return Info{Version: raw.Version, DeviceName: raw.DeviceName}, nil
}

type node struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	ChannelID   string `json:"channel_id"`
}

// Search finds content by keywords. ?keywords= works on every Kolibri
// version that has this API (0.20 also reads ?search=).
func (c *Client) Search(ctx context.Context, q string, max int) ([]Item, error) {
	if max <= 0 || max > 50 {
		max = 10
	}
	v := url.Values{"keywords": {q}, "max_results": {fmt.Sprint(max)}}
	var raw json.RawMessage
	if err := c.get(ctx, "/api/content/contentnode/?"+v.Encode(), &raw); err != nil {
		return nil, err
	}
	var nodes []node
	// Paginated ({"results": […]}) or a plain list, depending on version.
	var page struct {
		Results []node `json:"results"`
	}
	if err := json.Unmarshal(raw, &page); err == nil && page.Results != nil {
		nodes = page.Results
	} else if err := json.Unmarshal(raw, &nodes); err != nil {
		return nil, errors.New("Kolibri's answer was not understood")
	}
	out := []Item{}
	for _, n := range nodes {
		if n.ID == "" || n.Title == "" {
			continue
		}
		kind := "c"
		if n.Kind == "topic" {
			kind = "t"
		}
		desc := n.Description
		if r := []rune(desc); len(r) > 240 {
			desc = string(r[:240]) + "…"
		}
		link := c.BaseURL
		if c.PublicURL != "" {
			link = c.PublicURL
		}
		out = append(out, Item{ID: n.ID, Title: n.Title, Kind: n.Kind, Description: desc, ChannelID: n.ChannelID,
			URL: link + "/learn/#/topics/" + kind + "/" + url.PathEscape(n.ID)})
		if len(out) == max {
			break
		}
	}
	return out, nil
}
