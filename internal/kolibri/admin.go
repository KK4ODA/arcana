package kolibri

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrBadPassword means Kolibri refused the administrator's password, most
// likely because someone changed it in Kolibri.
var ErrBadPassword = errors.New("Kolibri refused the administrator's password; if it was changed in Kolibri, enter the new one on the Classroom page")

// Admin signs in to Kolibri as its administrator to manage content.
type Admin struct {
	BaseURL  string
	Username string
	Password string

	mu       sync.Mutex
	client   *http.Client
	signedIn bool
}

// Task is a Kolibri background task (an import).
type Task struct {
	ID string `json:"id"`
	// Status is Kolibri's: PENDING, SCHEDULED, QUEUED, SELECTED, RUNNING,
	// FAILED, CANCELING, CANCELED or COMPLETED.
	Status     string  `json:"status"`
	Type       string  `json:"type"`
	Percentage float64 `json:"percentage"`
	Exception  string  `json:"exception,omitempty"`
	Cancelable bool    `json:"cancellable"`
	Clearable  bool    `json:"clearable"`
	// ChannelID and ChannelName come from the task's metadata.
	ChannelID   string `json:"channelId"`
	ChannelName string `json:"channelName"`
	// FileSize and Transferred are bytes, when Kolibri reports them.
	FileSize    int64 `json:"fileSize,omitempty"`
	Transferred int64 `json:"transferred,omitempty"`
}

// Done reports whether the task has finished, one way or another.
func (t Task) Done() bool {
	switch t.Status {
	case "COMPLETED", "FAILED", "CANCELED":
		return true
	}
	return false
}

// ImportTaskType is Kolibri's task that imports a channel's database and
// the chosen resources from Kolibri Studio.
const ImportTaskType = "kolibri.core.content.tasks.remoteimport"

func (a *Admin) http() *http.Client {
	if a.client == nil {
		jar, _ := cookiejar.New(nil)
		a.client = &http.Client{Timeout: 30 * time.Second, Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return a.client
}

// signIn opens an administrator session (POST /api/auth/session/).
func (a *Admin) signIn(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": a.Username, "password": a.Password})
	resp, err := a.do(ctx, http.MethodPost, "/api/auth/session/", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest:
		return ErrBadPassword
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated:
		return fmt.Errorf("Kolibri answered %s when Arcana signed in", resp.Status)
	}
	a.signedIn = true
	return nil
}

// do sends one request with Kolibri's CSRF token (Django's csrftoken cookie
// echoed in X-CSRFToken), which Kolibri requires once signed in.
func (a *Admin) do(ctx context.Context, method, p string, body []byte) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.BaseURL+p, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Referer", a.BaseURL+"/")
	if u, err := url.Parse(a.BaseURL); err == nil {
		for _, c := range a.http().Jar.Cookies(u) {
			if c.Name == "csrftoken" {
				req.Header.Set("X-CSRFToken", c.Value)
			}
		}
	}
	resp, err := a.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach Kolibri: %w", err)
	}
	return resp, nil
}

// call sends a request as the administrator, signing in first and again
// once if the session has expired.
func (a *Admin) call(ctx context.Context, method, p string, in, out any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		if !a.signedIn {
			if err := a.signIn(ctx); err != nil {
				return err
			}
		}
		resp, err := a.do(ctx, method, p, body)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && attempt == 0 {
			a.signedIn = false
			continue
		}
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("Kolibri answered %s: %s", resp.Status, kolibriMessage(data))
		}
		if out == nil || len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		return json.Unmarshal(data, out)
	}
}

// kolibriMessage shortens an error body for people.
func kolibriMessage(data []byte) string {
	var list []struct {
		ID       string         `json:"id"`
		Metadata map[string]any `json:"metadata"`
	}
	if json.Unmarshal(data, &list) == nil && len(list) > 0 && list[0].ID != "" {
		return list[0].ID
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

type rawTask struct {
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	Type       string          `json:"type"`
	Percentage json.Number     `json:"percentage"`
	Exception  json.RawMessage `json:"exception"`
	Cancelable bool            `json:"cancellable"`
	Clearable  bool            `json:"clearable"`
	Extra      map[string]any  `json:"extra_metadata"`
}

func (r rawTask) task() Task {
	t := Task{ID: r.ID, Status: r.Status, Type: r.Type, Cancelable: r.Cancelable, Clearable: r.Clearable}
	if f, err := r.Percentage.Float64(); err == nil {
		t.Percentage = f
	}
	if len(r.Exception) > 0 && string(r.Exception) != "null" {
		var s string
		if json.Unmarshal(r.Exception, &s) == nil {
			t.Exception = s
		} else {
			t.Exception = string(r.Exception)
		}
	}
	str := func(k string) string { s, _ := r.Extra[k].(string); return s }
	num := func(k string) int64 { f, _ := r.Extra[k].(float64); return int64(f) }
	t.ChannelID, t.ChannelName = str("channel_id"), str("channel_name")
	t.FileSize, t.Transferred = num("file_size"), num("transferred_file_size")
	return t
}

// Import asks Kolibri to import a channel's database and the resources
// under nodeIDs (all of the channel when nodeIDs is empty) from Kolibri
// Studio.
func (a *Admin) Import(ctx context.Context, channelID, channelName string, nodeIDs []string) (Task, error) {
	if !IsHexID(channelID) {
		return Task{}, errors.New("not a channel ID")
	}
	for _, n := range nodeIDs {
		if !IsHexID(n) {
			return Task{}, errors.New("not a topic ID")
		}
	}
	req := map[string]any{"type": ImportTaskType, "channel_id": channelID, "channel_name": channelName}
	if len(nodeIDs) > 0 {
		req["node_ids"] = nodeIDs
	}
	var raw rawTask
	if err := a.call(ctx, http.MethodPost, "/api/tasks/tasks/", req, &raw); err != nil {
		return Task{}, err
	}
	return raw.task(), nil
}

// Tasks lists Kolibri's content tasks.
func (a *Admin) Tasks(ctx context.Context) ([]Task, error) {
	var raw []rawTask
	if err := a.call(ctx, http.MethodGet, "/api/tasks/tasks/?queue=content", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.task())
	}
	return out, nil
}

// Cancel asks Kolibri to stop a task.
func (a *Admin) Cancel(ctx context.Context, id string) error {
	if !IsTaskID(id) {
		return errors.New("not a task ID")
	}
	return a.call(ctx, http.MethodPost, "/api/tasks/tasks/"+id+"/cancel/", map[string]any{}, nil)
}

// Clear removes a finished task from Kolibri's list.
func (a *Admin) Clear(ctx context.Context, id string) error {
	if !IsTaskID(id) {
		return errors.New("not a task ID")
	}
	return a.call(ctx, http.MethodPost, "/api/tasks/tasks/"+id+"/clear/", map[string]any{}, nil)
}

// Check signs in again, to test a password.
func (a *Admin) Check(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.signedIn = false
	return a.signIn(ctx)
}

// IsHexID reports whether s is a 32-digit hexadecimal ID, the form of
// Kolibri's channel and node IDs.
func IsHexID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// IsTaskID reports whether s looks like one of Kolibri's task IDs.
func IsTaskID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
