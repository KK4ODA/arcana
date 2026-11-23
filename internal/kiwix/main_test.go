package kiwix

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for kiwix-serve: with
// ARCANA_TEST_KIWIX_EXIT set it exits at once with that status, the way a
// program the system cannot load does. With ARCANA_TEST_KIWIX_SLOW set to a
// duration it behaves like a kiwix-serve that needs that long to open its
// archives before it answers.
func TestMain(m *testing.M) {
	if code := os.Getenv("ARCANA_TEST_KIWIX_EXIT"); code != "" {
		n, _ := strconv.ParseUint(code, 0, 32)
		os.Exit(int(int32(uint32(n))))
	}
	if d := os.Getenv("ARCANA_TEST_KIWIX_SLOW"); d != "" {
		slowKiwix(d)
	}
	os.Exit(m.Run())
}

// slowKiwix waits, then serves a catalog listing every archive it was given.
func slowKiwix(delay string) {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		os.Exit(0)
	}
	d, _ := time.ParseDuration(delay)
	time.Sleep(d)
	var port string
	var names []string
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "--port=") {
			port = strings.TrimPrefix(a, "--port=")
		} else if !strings.HasPrefix(a, "--") {
			names = append(names, BookName(a))
		}
	}
	http.HandleFunc(URLRoot+"/catalog/v2/entries", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<feed>")
		for _, n := range names {
			fmt.Fprintf(w, `<entry><id>urn:uuid:%s</id><title>%s</title><link type="text/html" href="%s/content/%s"/></entry>`, n, n, URLRoot, n)
		}
		fmt.Fprint(w, "</feed>")
	})
	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, nil))
}
