package kolibri

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/kolibri/kolibritest"
)

const (
	tChannel = kolibritest.ChannelID
	tGrade1  = kolibritest.Grade1
	tGrade2  = kolibritest.Grade2
	tArt     = kolibritest.Art
)

func makeChannelDB(t *testing.T, p, channelID string) {
	t.Helper()
	if err := kolibritest.WriteChannelDB(p, channelID); err != nil {
		t.Fatal(err)
	}
}

func TestChannelTreeAndSizes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "es.sqlite3")
	makeChannelDB(t, p, tChannel)
	c, err := OpenChannelDB(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.ID != tChannel || c.Ver != 14 {
		t.Fatalf("channel %s v%d", c.ID, c.Ver)
	}
	ctx := context.Background()
	root, err := c.Tree(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	// The shared video counts once in the channel and in Math.
	if root.Size != 1510 || root.Resources != 3 || len(root.Children) != 2 {
		t.Fatalf("root %+v", root)
	}
	math := root.Children[0]
	if math.Title != "Matemáticas" || math.Size != 1010 || len(math.Children) != 2 || math.Children[1].Size != 1000 {
		t.Fatalf("math %+v", math)
	}
	// Both grades together: the shared video once.
	if size, n, err := c.SizeOf(ctx, []string{tGrade1, tGrade2, tGrade1}); err != nil || size != 1010 || n != 2 {
		t.Fatalf("grades %d %d %v", size, n, err)
	}
	if size, _, err := c.SizeOf(ctx, []string{tGrade2, tArt}); err != nil || size != 1500 {
		t.Fatalf("grade 2 and art %d %v", size, err)
	}
	if _, _, err := c.SizeOf(ctx, []string{"f0000000000000000000000000000000"}); err == nil {
		t.Fatal("a topic outside the channel was measured")
	}
	// The root topic is the whole channel: the picker's "Everything".
	if size, n, err := c.SizeOf(ctx, []string{kolibritest.Root}); err != nil || size != 1510 || n != 3 {
		t.Fatalf("whole channel %d %d %v", size, n, err)
	}
	if err := c.Has(ctx, []string{tArt, tGrade1, kolibritest.Root}); err != nil {
		t.Fatal(err)
	}
	if err := c.Has(ctx, []string{"f0000000000000000000000000000000"}); err == nil {
		t.Fatal("Has accepted a stranger")
	}
}

// TestOfferedChannels checks the list the Classroom page shows: each
// channel can be fetched and imported by its ID, sits in a group the page
// shows, and says who it is for, what it holds, how big it is and how it is
// licensed, with non-commercial licences saying so in words.
func TestOfferedChannels(t *testing.T) {
	if Channels[0].ID != "c9d7f950ab6b5a1199e3d6c10d7f0103" || Channels[0].Group != GroupKhan {
		t.Fatalf("Khan Academy in English (US curriculum) is not first: %+v", Channels[0])
	}
	lang := regexp.MustCompile(`^[a-z]{2,3}(-[A-Z]{2})?$`)
	seen := map[string]bool{}
	groups := map[string]int{}
	for _, c := range Channels {
		if !IsHexID(c.ID) || seen[c.ID] {
			t.Errorf("%q: bad or repeated ID", c.ID)
		}
		seen[c.ID] = true
		if got, ok := ChannelByID(c.ID); !ok || got.Title != c.Title {
			t.Errorf("%s: ChannelByID %v %q", c.ID, ok, got.Title)
		}
		switch c.Group {
		case GroupKhan, GroupMore, GroupKhanWorld:
			groups[c.Group]++
		default:
			t.Errorf("%s: unknown group %q", c.Title, c.Group)
		}
		if c.Title == "" || c.Levels == "" || c.About == "" || c.License == "" || c.Size <= 0 {
			t.Errorf("%s: incomplete %+v", c.ID, c)
		}
		if !lang.MatchString(c.Language) {
			t.Errorf("%s: language %q", c.Title, c.Language)
		}
		if _, err := time.Parse("2006-01-02", c.Published); err != nil {
			t.Errorf("%s: published %q", c.Title, c.Published)
		}
		if strings.Contains(c.License, "-NC") != strings.Contains(c.License, "non-commercial use only") {
			t.Errorf("%s: licence %q does not say whether it is non-commercial", c.Title, c.License)
		}
		if khan := strings.HasPrefix(c.Title, "Khan Academy"); khan == (c.Group == GroupMore) {
			t.Errorf("%s: in group %s", c.Title, c.Group)
		}
	}
	for _, g := range []string{GroupKhan, GroupMore, GroupKhanWorld} {
		if groups[g] == 0 {
			t.Errorf("group %s is empty", g)
		}
	}
}

func TestFetchChannelDB(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.sqlite3")
	makeChannelDB(t, good, tChannel)
	other := filepath.Join(dir, "other.sqlite3")
	makeChannelDB(t, other, "d0000000000000000000000000000000")
	studio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/content/databases/" + tChannel + ".sqlite3":
			http.ServeFile(w, r, good)
		case "/content/databases/" + Channels[0].ID + ".sqlite3":
			http.ServeFile(w, r, other) // the wrong channel
		default:
			http.NotFound(w, r)
		}
	}))
	defer studio.Close()
	ctx := context.Background()
	dest := filepath.Join(dir, "cache", tChannel+".sqlite3")
	if err := FetchChannelDB(ctx, studio.Client(), studio.URL, tChannel, dest); err != nil {
		t.Fatal(err)
	}
	if StaleChannelDB(dest) {
		t.Fatal("just fetched, already stale")
	}
	wrong := filepath.Join(dir, "cache", "en.sqlite3")
	if err := FetchChannelDB(ctx, studio.Client(), studio.URL, Channels[0].ID, wrong); err == nil || !strings.Contains(err.Error(), "instead of") {
		t.Fatalf("wrong channel accepted: %v", err)
	}
	if _, err := os.Stat(wrong); !os.IsNotExist(err) {
		t.Fatal("wrong channel kept")
	}
	if err := FetchChannelDB(ctx, studio.Client(), studio.URL, "../../etc", wrong); err == nil {
		t.Fatal("a bad ID was fetched")
	}
}
