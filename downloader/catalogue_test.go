// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/dappcore/go"
	process "github.com/dappcore/go-process/go"

	"manga/opds"
	"manga/shelf"
)

// fakeCatalogue serves a Komga-shaped series of two books; books whose path
// contains "slow" take ten seconds, "broken" ones fail.
func fakeCatalogue(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/series":
			w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom">
				<entry><title>Vol. 1</title><link rel="http://opds-spec.org/acquisition" href="/files/Frieren%20v01.cbz" type="application/zip"/></entry>
				<entry><title>Vol. 2</title><link rel="http://opds-spec.org/acquisition" href="/files/Frieren%20v02.cbz" type="application/zip"/></entry>
			</feed>`))
		case strings.Contains(r.URL.Path, "slow"):
			select { // gives up when Pinku hangs up, as a real server would
			case <-time.After(10 * time.Second):
			case <-r.Context().Done():
			}
		case strings.Contains(r.URL.Path, "broken"):
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/files/"):
			w.Write([]byte("PK book " + r.URL.Path))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func catalogueDownloader(t *testing.T, srv *httptest.Server) (*Service, string) {
	t.Helper()
	lib := filepath.Join(t.TempDir(), "Manga")
	set := shelf.Settings{Library: lib, Language: "en", Format: "cbz-volume"}
	cats := map[string]opds.Catalogue{"home": {URL: srv.URL + "/opds"}}
	c := core.New(
		core.WithService(process.Register),
		core.WithService(Register(Options{DataDir: t.TempDir(), Settings: func() shelf.Settings { return set },
			Catalogue: func(id string) (opds.Catalogue, bool) { c, ok := cats[id]; return c, ok }})),
		core.WithServiceLock(),
	)
	if r := c.ServiceStartup(core.Background(), nil); !r.OK {
		t.Fatal(r.Error())
	}
	t.Cleanup(func() { c.ServiceShutdown(core.Background()) })
	return core.MustServiceFor[*Service](c, "downloader"), lib
}

// A series from a catalogue: both books saved under their own file names,
// manga.json says where it came from, and checking again fetches nothing new.
func TestCatalogueSeries(t *testing.T) {
	srv := fakeCatalogue(t)
	d, lib := catalogueDownloader(t, srv)
	j, err := d.AddFromCatalogue("home", opds.Entry{Title: "Frieren", Feed: srv.URL + "/series"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the books", func() bool { return job(d, j.ID).State == "done" })
	if got := job(d, j.ID); got.Volumes != 2 {
		t.Fatalf("job: %+v\nlog: %v", got, d.Log(j.ID))
	}
	for _, name := range []string{"Frieren v01.cbz", "Frieren v02.cbz"} {
		if b, err := os.ReadFile(filepath.Join(lib, "Frieren", name)); err != nil || !strings.HasPrefix(string(b), "PK book") {
			t.Errorf("%s: %q %v", name, b, err)
		}
	}
	again, err := d.AddFromCatalogue("home", opds.Entry{Title: "Frieren", Feed: srv.URL + "/series"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the re-check", func() bool { return job(d, again.ID).State == "done" })
	if got := job(d, again.ID); got.Folder != "Frieren" || got.Volumes != 0 || !strings.Contains(strings.Join(d.Log(again.ID), "\n"), "already on the shelf") {
		t.Errorf("re-check: %+v %v", got, d.Log(again.ID))
	}
}

// One book picked from the results (no series feed): saved, and there's
// nothing to check for later.
func TestCatalogueSingleBook(t *testing.T) {
	srv := fakeCatalogue(t)
	d, lib := catalogueDownloader(t, srv)
	j, err := d.AddFromCatalogue("home", opds.Entry{Title: "Yotsuba&! Vol. 1", Books: []opds.Book{{Title: "Yotsuba&! Vol. 1", File: srv.URL + "/files/42", Format: "epub"}}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the book", func() bool { return job(d, j.ID).State == "done" })
	if _, err := os.Stat(filepath.Join(lib, "Yotsuba&! Vol. 1", "Yotsuba&! Vol. 1.epub")); err != nil {
		t.Errorf("not saved: %v", err)
	}
	if _, err := d.Update("Yotsuba&! Vol. 1"); err == nil {
		t.Error("a single book has nothing to check for")
	}
}

// A book that won't download fails the job saying which; Stop stops one
// mid-download; unknown catalogues and empty entries are refused.
func TestCatalogueTrouble(t *testing.T) {
	srv := fakeCatalogue(t)
	d, lib := catalogueDownloader(t, srv)
	bad, _ := d.AddFromCatalogue("home", opds.Entry{Title: "Broken", Books: []opds.Book{{Title: "x", File: srv.URL + "/files/broken.cbz", Format: "cbz"}}})
	waitFor(t, "it to fail", func() bool { return job(d, bad.ID).State == "failed" })
	if e := job(d, bad.ID).Error; !strings.Contains(e, "broken.cbz") {
		t.Errorf("error = %q", e)
	}
	slow, _ := d.AddFromCatalogue("home", opds.Entry{Title: "Slow", Books: []opds.Book{{Title: "x", File: srv.URL + "/files/slow.cbz", Format: "cbz"}}})
	waitFor(t, "it to start", func() bool { return job(d, slow.ID).State == "running" && job(d, slow.ID).Now != "" })
	start := time.Now()
	d.Cancel(slow.ID)
	waitFor(t, "it to stop", func() bool { return job(d, slow.ID).State == "cancelled" && !job(d, slow.ID).Finished.IsZero() })
	if time.Since(start) > 5*time.Second {
		t.Error("Stop waited for the download")
	}
	if left, _ := filepath.Glob(filepath.Join(lib, "Slow", ".saving-*")); len(left) != 0 {
		t.Errorf("half a book left: %v", left)
	}
	if _, err := d.AddFromCatalogue("gone", opds.Entry{Title: "X", Feed: srv.URL + "/series"}); err == nil {
		t.Error("an unknown catalogue")
	}
	if _, err := d.AddFromCatalogue("home", opds.Entry{Title: "Nothing"}); err == nil {
		t.Error("an entry with no books or feed")
	}
}

func TestBookFile(t *testing.T) {
	for _, c := range []struct {
		b    opds.Book
		want string
	}{
		{opds.Book{Title: "Vol. 1", File: "https://k/books/1/file/Frieren%20v01.cbz", Format: "cbz"}, "Frieren v01.cbz"},
		{opds.Book{Title: "Yotsuba: Vol. 1?", File: "https://c/get/epub/42", Format: "epub"}, "Yotsuba- Vol. 1.epub"},
		{opds.Book{File: "https://c/get/42", Format: "pdf"}, "Book.pdf"},
	} {
		if got := BookFile(c.b); got != c.want {
			t.Errorf("BookFile(%+v) = %q, want %q", c.b, got, c.want)
		}
	}
}
