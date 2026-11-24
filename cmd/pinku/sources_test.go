// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKomga answers like Komga, from opds/testdata, behind a login.
func fakeKomga(t *testing.T) *httptest.Server {
	t.Helper()
	routes := map[string]string{
		"/opds/v1.2/catalog":     "komga-root.xml",
		"/opds/v1.2/search":      "komga-search.xml",
		"/opds/v1.2/series/0A1B": "komga-series.xml",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "her" || p != "pw" {
			w.WriteHeader(401)
			return
		}
		if file, ok := routes[r.URL.Path]; ok && r.URL.RawQuery != "page=1" {
			b, _ := os.ReadFile("../../opds/testdata/" + file)
			w.Write(b)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/thumbnail"):
			w.Write([]byte("\x89PNG\r\n\x1a\n a cover"))
		case strings.HasPrefix(r.URL.Path, "/opds/v1.2/books/01/file/"):
			w.Write([]byte("PK frieren volume one"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func addSource(t *testing.T, srv *httptest.Server, body string) (sourceView, string) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/api/sources", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Source sourceView
		Check  string
	}
	json.NewDecoder(resp.Body).Decode(&got)
	if resp.StatusCode != 200 {
		t.Fatalf("add source: %d", resp.StatusCode)
	}
	return got.Source, got.Check
}

// Her catalogue, from Settings to the bookcase: added (and tried), listed
// without its password, searched with covers through the app, a series put
// on the shelf, and checked for new books like a MangaDex one.
func TestCatalogueSource(t *testing.T) {
	komga := fakeKomga(t)
	a, srv, lib := flowApp(t)
	src, check := addSource(t, srv, `{"name":"Home","url":"`+komga.URL+`/opds/v1.2/catalog","user":"her","password":"pw"}`)
	if check != "" || src.ID == "" || !src.HasPassword {
		t.Fatalf("added: %+v check %q", src, check)
	}
	var listed string
	fetch(t, srv.URL+"/api/sources", &listed)
	if strings.Contains(listed, `"pw"`) || !strings.Contains(listed, "Home") {
		t.Errorf("the list gave away the password, or lost the catalogue: %s", listed)
	}

	var found []catalogueResults
	if fetch(t, srv.URL+"/api/sources/search?q=frieren", &found) != 200 || len(found) != 1 || found[0].Error != "" || len(found[0].Results) != 1 {
		t.Fatalf("search: %+v", found)
	}
	series := found[0].Results[0]
	if !strings.HasPrefix(series.Cover, "/api/sources/"+src.ID+"/cover?url=") {
		t.Errorf("cover not through the app: %s", series.Cover)
	}
	resp, _ := http.Get(srv.URL + series.Cover)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("cover proxy: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if fetch(t, srv.URL+"/api/sources/"+src.ID+"/cover?url=https://elsewhere.example/x.png", nil) != 404 {
		t.Error("the cover proxy fetched from another server")
	}

	body, _ := json.Marshal(series)
	if post(t, srv.URL+"/api/sources/"+src.ID+"/add", string(body)) != 200 {
		t.Fatal("add refused")
	}
	jobs := jobsUntil(t, srv, allSettled)
	if jobs[0].State != "done" || jobs[0].Volumes != 1 {
		t.Fatalf("download: %+v\n%v", jobs[0], a.down.Log(jobs[0].ID))
	}
	if b, _ := os.ReadFile(filepath.Join(lib, jobs[0].Folder, "Frieren v01.cbz")); string(b) != "PK frieren volume one" {
		t.Errorf("the book on disk: %q", b)
	}
	var sh struct {
		Series []struct {
			Folder, Source string
			Checkable      bool
		}
	}
	fetch(t, srv.URL+"/api/shelf", &sh)
	if len(sh.Series) != 1 || sh.Series[0].Source != "opds" || !sh.Series[0].Checkable {
		t.Errorf("on the shelf: %+v", sh)
	}
	if post(t, srv.URL+"/api/update", `{"folder":"`+jobs[0].Folder+`"}`) != 200 {
		t.Error("checking a catalogue series for new books")
	}
	jobsUntil(t, srv, allSettled)

	if post(t, srv.URL+"/api/sources/"+src.ID+"/remove", `{}`) != 204 || len(a.shelf.Sources()) != 0 {
		t.Error("remove")
	}
}

// A catalogue that's down or refuses her login says so in its own results;
// a link that isn't the web isn't saved; adding from an unknown catalogue
// is refused.
func TestCatalogueSourceTrouble(t *testing.T) {
	komga := fakeKomga(t)
	_, srv, _ := flowApp(t)
	_, check := addSource(t, srv, `{"name":"Wrong login","url":"`+komga.URL+`/opds/v1.2/catalog","user":"her","password":"nope"}`)
	if !strings.Contains(check, "username") {
		t.Errorf("a wrong login should be caught when it's added: %q", check)
	}
	addSource(t, srv, `{"name":"Right","url":"`+komga.URL+`/opds/v1.2/catalog","user":"her","password":"pw"}`)
	var found []catalogueResults
	fetch(t, srv.URL+"/api/sources/search?q=frieren", &found)
	if len(found) != 2 || (found[0].Error == "") == (found[1].Error == "") {
		t.Errorf("one broken, one fine: %+v", found)
	}
	if post(t, srv.URL+"/api/sources", `{"url":"file:///etc/passwd"}`) != 400 {
		t.Error("saved a file: link")
	}
	if post(t, srv.URL+"/api/sources/nope/add", `{"title":"x","feed":"`+komga.URL+`/x"}`) != 400 || post(t, srv.URL+"/api/sources/nope/add", `{garbage`) != 400 {
		t.Error("add from an unknown catalogue / garbage")
	}
}
