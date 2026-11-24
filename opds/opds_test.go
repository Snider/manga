// SPDX-License-Identifier: EUPL-1.2

package opds

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// server answers like a catalogue, from testdata; it records what it was
// asked and whether the request carried her login.
type server struct {
	*httptest.Server
	asked []string
	authd map[string]bool
}

func catalogue(t *testing.T, routes map[string]string) *server {
	t.Helper()
	s := &server{authd: map[string]bool{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.asked = append(s.asked, r.URL.RequestURI())
		_, _, ok := r.BasicAuth()
		s.authd[r.URL.Path] = ok
		file, found := routes[r.URL.RequestURI()]
		if !found {
			file, found = routes[r.URL.Path]
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(file, "data:") {
			w.Write([]byte(strings.TrimPrefix(file, "data:")))
			return
		}
		b, err := os.ReadFile("testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}))
	t.Cleanup(s.Close)
	return s
}

// Komga: search through the root feed's template, a series with a cover and a
// feed, its books across two pages, the file fetched.
func TestKomga(t *testing.T) {
	s := catalogue(t, map[string]string{
		"/opds/v1.2/catalog":                         "komga-root.xml",
		"/opds/v1.2/search":                          "komga-search.xml",
		"/opds/v1.2/series/0A1B":                     "komga-series.xml",
		"/opds/v1.2/series/0A1B?page=1":              "komga-series-page2.xml",
		"/opds/v1.2/books/01/file/Frieren%20v01.cbz": "data:PK cbz bytes",
	})
	c := Catalogue{URL: s.URL + "/opds/v1.2/catalog", User: "her", Password: "pw"}
	found, err := c.Search(context.Background(), "frieren beyond")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Title != "Frieren: Beyond Journey's End" || found[0].Author != "Kanehito Yamada" ||
		found[0].Cover != s.URL+"/opds/v1.2/series/0A1B/thumbnail" || found[0].Feed != s.URL+"/opds/v1.2/series/0A1B" {
		t.Fatalf("search: %+v", found)
	}
	if !strings.Contains(strings.Join(s.asked, " "), "/opds/v1.2/search?search=frieren+beyond") {
		t.Errorf("searched with %v", s.asked)
	}
	books, err := c.Books(context.Background(), found[0].Feed)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 || books[0].Format != "cbz" || books[0].Size != 1024 || books[1].File != "https://cdn.elsewhere.example/Frieren%20v02.cbz" {
		t.Fatalf("books: %+v", books)
	}
	var file bytes.Buffer
	if err := c.Fetch(context.Background(), books[0].File, &file); err != nil || file.String() != "PK cbz bytes" {
		t.Errorf("fetch: %q %v", file.String(), err)
	}
	if !s.authd["/opds/v1.2/catalog"] {
		t.Error("her login wasn't sent to her own catalogue")
	}
}

// Calibre: search through an OpenSearch description, into a path; books
// right in the results; formats Pinku can't shelve (mobi) left out.
func TestCalibre(t *testing.T) {
	s := catalogue(t, map[string]string{
		"/opds":                     "calibre-root.xml",
		"/opds/search/osd":          "calibre-osd.xml",
		"/opds/search/yotsuba%20to": "calibre-search.xml",
	})
	c := Catalogue{URL: s.URL + "/opds"}
	found, err := c.Search(context.Background(), "yotsuba to")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Title != "Yotsuba&! Vol. 1" || found[0].Summary != "Yotsuba moves to a new town." {
		t.Fatalf("search: %+v", found)
	}
	if b := found[0].Books; len(b) != 1 || b[0].Format != "epub" || b[0].File != s.URL+"/get/epub/42" {
		t.Errorf("books: %+v", b)
	}
}

// Her login goes to her catalogue's host only, never to a link elsewhere.
func TestLoginStaysHome(t *testing.T) {
	elsewhere := catalogue(t, map[string]string{"/book.cbz": "data:x"})
	home := catalogue(t, map[string]string{"/opds": "calibre-root.xml"})
	c := Catalogue{URL: home.URL + "/opds", User: "her", Password: "pw"}
	var b bytes.Buffer
	if err := c.Fetch(context.Background(), elsewhere.URL+"/book.cbz", &b); err != nil {
		t.Fatal(err)
	}
	if elsewhere.authd["/book.cbz"] {
		t.Error("her login was sent to another host")
	}
}

// What a catalogue can get wrong: no search, not OPDS, a wrong login, a link
// that isn't the web, an empty query.
func TestCatalogueTrouble(t *testing.T) {
	s := catalogue(t, map[string]string{
		"/nosearch": "komga-series.xml",
		"/html":     "data:<html><body>a web page</body>",
		"/osd-html": "data:<feed xmlns=\"http://www.w3.org/2005/Atom\"><link rel=\"search\" type=\"application/opensearchdescription+xml\" href=\"/osd\"/></feed>",
		"/osd":      "data:<OpenSearchDescription><Url type=\"text/html\" template=\"/q?{searchTerms}\"/></OpenSearchDescription>",
		"/locked":   "data:",
	})
	ctx := context.Background()
	for name, c := range map[string]Catalogue{
		"no search link":      {URL: s.URL + "/nosearch"},
		"not OPDS":            {URL: s.URL + "/html"},
		"OpenSearch, no atom": {URL: s.URL + "/osd-html"},
		"missing":             {URL: s.URL + "/gone"},
		"not the web":         {URL: "file:///etc/passwd"},
		"unreachable":         {URL: "http://127.0.0.1:1/opds"},
	} {
		if _, err := c.Search(ctx, "x"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got, err := (Catalogue{URL: s.URL + "/nosearch"}).Search(ctx, "  "); got != nil || err != nil {
		t.Errorf("empty query: %v %v", got, err)
	}
	locked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer locked.Close()
	if _, err := (Catalogue{URL: locked.URL}).Search(ctx, "x"); err == nil || !strings.Contains(err.Error(), "username") {
		t.Errorf("401: %v", err)
	}
	if _, err := (Catalogue{URL: s.URL}).Books(ctx, s.URL+"/gone"); err == nil {
		t.Error("books of a missing feed")
	}
	var b bytes.Buffer
	if err := (Catalogue{URL: s.URL}).Fetch(ctx, s.URL+"/gone", &b); err == nil {
		t.Error("fetching a missing file")
	}
}
