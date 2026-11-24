// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// When something goes wrong the page gets a clear status and an error to
// show, never a crash or a half-answer.
func TestAPIWhenThingsGoWrong(t *testing.T) {
	a, srv, lib := flowApp(t)

	// Garbage instead of JSON, to every endpoint that reads a body.
	for _, path := range []string{"/api/progress", "/api/open", "/api/browse", "/api/add", "/api/update", "/api/settings"} {
		if code := post(t, srv.URL+path, `{not json`); code != 400 {
			t.Errorf("POST %s with garbage = %d, want 400", path, code)
		}
	}

	// The window's own calls failing.
	oops := errors.New("the window said no")
	a.browse = func(string) error { return oops }
	a.open = func(string) error { return oops }
	a.pick = func(string) (string, error) { return "", oops }
	a.pickBooks = func() ([]string, error) { return nil, oops }
	if post(t, srv.URL+"/api/browse", `{"url":"https://mangadex.org"}`) != 500 {
		t.Error("a browser that won't open should be a 500")
	}
	if post(t, srv.URL+"/api/open", `{}`) != 400 {
		t.Error("Explorer failing to open should be reported")
	}
	if post(t, srv.URL+"/api/pick-folder", `{}`) != 500 || post(t, srv.URL+"/api/import", `{}`) != 500 {
		t.Error("a dialog failing should be a 500")
	}

	// A series that isn't there, and its cover.
	if fetch(t, srv.URL+"/api/cover/Nothing%20Here", nil) != 404 {
		t.Error("cover of a missing series")
	}
	if post(t, srv.URL+"/api/open", `{"folder":"a/b"}`) != 400 {
		t.Error("opening a folder name with a slash")
	}

	// MangaDex down: the search says so (502), not "nothing found".
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer down.Close()
	a.dex.BaseURL = down.URL
	if fetch(t, srv.URL+"/api/search?q=uso", nil) != 502 {
		t.Error("MangaDex down should be a 502")
	}

	// The bookcase folder turned into a file: the shelf can't be read.
	os.RemoveAll(lib)
	os.WriteFile(lib, []byte("not a folder"), 0o644)
	if code := fetch(t, srv.URL+"/api/shelf", nil); code != 500 {
		t.Errorf("an unreadable bookcase = %d, want 500", code)
	}
	if post(t, srv.URL+"/api/add", `{"id":"`+usoID+`","title":"Anything"}`) != 400 {
		t.Error("adding with no bookcase folder to make it in should be refused")
	}
	os.Remove(lib)
}

// An empty search is an empty list (the page shows "nothing called that"),
// and the version answer is there even for a dev build.
func TestAPIEmptyAnswers(t *testing.T) {
	a, srv, _ := flowApp(t)
	none := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":"ok","data":[]}`))
	}))
	defer none.Close()
	a.dex.BaseURL = none.URL
	var found struct{ Results []any }
	if fetch(t, srv.URL+"/api/search?q=zzzz", &found) != 200 || found.Results == nil || len(found.Results) != 0 {
		t.Errorf("no results: %+v", found)
	}
	var v struct {
		Version string
		Newer   bool
	}
	if fetch(t, srv.URL+"/api/version", &v) != 200 || v.Version != "dev" || v.Newer {
		t.Errorf("version: %+v", v)
	}
	a.words = nil
	var s struct {
		Lang    string
		Strings map[string]string
	}
	if fetch(t, srv.URL+"/api/strings", &s) != 200 || s.Lang != "en" {
		t.Errorf("strings without translations: %+v", s)
	}
}
