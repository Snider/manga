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
	"time"

	"manga/downloader"
	"manga/internal/fakedl"
)

// The test binary doubles as mangadex-dl (see internal/fakedl), so the whole
// add → download → shelf path runs in these tests, on every OS.
func TestMain(m *testing.M) {
	fakedl.Run()
	os.Exit(m.Run())
}

const usoID = "cbe84ea7-2992-4db0-b4ef-d042124f48c8" // the one manga in mangadex/testdata/search.json

// fakeDex answers like MangaDex, from the real answers in mangadex/testdata.
func fakeDex(t *testing.T) *httptest.Server {
	t.Helper()
	search, err := os.ReadFile("../../mangadex/testdata/search.json")
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Data []json.RawMessage }
	json.Unmarshal(search, &list)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/manga":
			w.Write(search)
		case r.URL.Path == "/manga/"+usoID:
			w.Write([]byte(`{"result":"ok","data":` + string(list.Data[0]) + `}`))
		case strings.HasPrefix(r.URL.Path, "/covers/"+usoID+"/"):
			w.Write([]byte("\xff\xd8\xff\xe0 a jpeg"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// flowApp is testApp with the fake MangaDex and the fake mangadex-dl.
func flowApp(t *testing.T) (*app, *httptest.Server, string) {
	t.Helper()
	lib := filepath.Join(t.TempDir(), "Manga")
	a, srv := testApp(t, lib)
	dex := fakeDex(t)
	a.dex.BaseURL, a.dex.UploadsURL = dex.URL, dex.URL
	set := a.shelf.Settings()
	set.Tool = fakedl.Tool(t)
	if r := a.shelf.Save(set); !r.OK {
		t.Fatal(r.Error())
	}
	return a, srv, lib
}

func jobsUntil(t *testing.T, srv *httptest.Server, done func([]downloader.Job) bool) []downloader.Job {
	t.Helper()
	var jobs []downloader.Job
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		jobs = nil
		fetch(t, srv.URL+"/api/jobs", &jobs)
		if done(jobs) {
			return jobs
		}
	}
	t.Fatalf("jobs never settled: %+v", jobs)
	return nil
}

func allSettled(jobs []downloader.Job) bool {
	for _, j := range jobs {
		if j.State == "queued" || j.State == "running" {
			return false
		}
	}
	return len(jobs) > 0
}

// What she actually does: search, put one on the shelf, it downloads, it's
// on the bookcase, search now says it's hers, and checking for new chapters
// works for it alone and for everything.
func TestSearchAddDownloadCheck(t *testing.T) {
	_, srv, lib := flowApp(t)
	var found struct {
		Results []struct{ ID, Title string }
		Owned   map[string]string
	}
	if fetch(t, srv.URL+"/api/search?q=uso", &found) != 200 || len(found.Results) != 1 || found.Results[0].ID != usoID || len(found.Owned) != 0 {
		t.Fatalf("search: %+v", found)
	}
	pasted := found
	if fetch(t, srv.URL+"/api/search?q=https://mangadex.org/title/"+usoID+"/uso-douri", &pasted) != 200 || len(pasted.Results) != 1 {
		t.Fatalf("a pasted link: %+v", pasted)
	}

	body, _ := json.Marshal(found.Results[0])
	if post(t, srv.URL+"/api/add", string(body)) != 200 {
		t.Fatal("add refused")
	}
	jobs := jobsUntil(t, srv, allSettled)
	if jobs[0].State != "done" || jobs[0].Pages != 2 {
		t.Fatalf("download: %+v", jobs[0])
	}
	folder := jobs[0].Folder
	if _, err := os.Stat(filepath.Join(lib, folder, "Vol. 1.cbz")); err != nil {
		t.Fatalf("no volume on disk: %v", err)
	}
	var log []string
	if fetch(t, srv.URL+"/api/jobs/"+itoa(jobs[0].ID)+"/log", &log) != 200 || len(log) == 0 {
		t.Error("the job's log is empty")
	}

	var sh struct {
		Series []struct{ Folder, Mangadex string }
	}
	fetch(t, srv.URL+"/api/shelf", &sh)
	if len(sh.Series) != 1 || sh.Series[0].Folder != folder {
		t.Fatalf("bookcase: %+v", sh)
	}
	fetch(t, srv.URL+"/api/search?q=uso", &found)
	if found.Owned[usoID] != folder {
		t.Errorf("search doesn't know it's hers: %+v", found.Owned)
	}

	if post(t, srv.URL+"/api/update", `{"folder":"`+folder+`"}`) != 200 {
		t.Error("checking one series refused")
	}
	jobsUntil(t, srv, allSettled)
	var all []downloader.Job
	if fetch(t, srv.URL+"/api/update", nil) == 200 {
		t.Error("update answers GET")
	}
	resp, _ := http.Post(srv.URL+"/api/update", "application/json", strings.NewReader(`{"folder":""}`))
	json.NewDecoder(resp.Body).Decode(&all)
	resp.Body.Close()
	if len(all) != 1 || all[0].Folder != folder {
		t.Errorf("check everything: %+v", all)
	}
	jobsUntil(t, srv, allSettled)
	if post(t, srv.URL+"/api/update", `{"folder":"no such series"}`) != 400 {
		t.Error("checking a series that isn't there should be refused")
	}
}

// Stop from the Downloads tab stops it.
func TestStopFromTheDownloadsTab(t *testing.T) {
	_, srv, _ := flowApp(t)
	if post(t, srv.URL+"/api/add", `{"id":"`+usoID+`","title":"SLOW to arrive"}`) != 200 {
		t.Fatal("add refused")
	}
	jobs := jobsUntil(t, srv, func(js []downloader.Job) bool { return len(js) == 1 && js[0].State == "running" })
	if post(t, srv.URL+"/api/jobs/"+itoa(jobs[0].ID)+"/cancel", `{}`) != 204 {
		t.Fatal("cancel refused")
	}
	jobs = jobsUntil(t, srv, allSettled)
	if jobs[0].State != "cancelled" {
		t.Errorf("after Stop: %+v", jobs[0])
	}
}

// Covers come through the app (the window never fetches from elsewhere), and
// only real MangaDex covers.
func TestCoverProxy(t *testing.T) {
	_, srv, _ := flowApp(t)
	resp, err := http.Get(srv.URL + "/api/dexcover/" + usoID + "/0f5e0c47-6e41-4ad0-bb5e-6e4b4c7cc5f6.jpg")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("cover: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, path := range []string{"/api/dexcover/" + usoID + "/..%2F..%2Fsecret.jpg", "/api/dexcover/not-an-id/x.jpg", "/api/dexcover/" + usoID + "/cover.exe"} {
		if code := fetch(t, srv.URL+path, nil); code == 200 {
			t.Errorf("GET %s = 200", path)
		}
	}
}

// Settings come back as saved, with the choices the page needs.
func TestSettingsRoundTrip(t *testing.T) {
	a, srv, lib := flowApp(t)
	var got struct {
		Settings struct{ Library, Language, Format, Words string }
		Formats  map[string]string
		Tool     string
	}
	fetch(t, srv.URL+"/api/settings", &got)
	if got.Settings.Library != lib || len(got.Formats) != 2 || got.Tool == "" {
		t.Fatalf("settings: %+v", got)
	}
	body := `{"library":` + quote(lib) + `,"language":"tr","format":"cbz","tool":"","words":"tr"}`
	if post(t, srv.URL+"/api/settings", body) != 200 {
		t.Fatal("valid settings refused")
	}
	if s := a.shelf.Settings(); s.Language != "tr" || s.Format != "cbz" || s.Words != "tr" {
		t.Errorf("saved: %+v", s)
	}
	for _, bad := range []string{
		`{"library":` + quote(lib) + `,"language":"tr","format":"mp4"}`,
		`{"library":` + quote(lib) + `,"language":"Turkish!","format":"cbz"}`,
		`not json`,
	} {
		if post(t, srv.URL+"/api/settings", bad) != 400 {
			t.Errorf("accepted %s", bad)
		}
	}
}

// The window's own dialogs: picking a folder, picking books to import, and
// opening the bookcase or a book in Explorer.
func TestDialogsAndOpening(t *testing.T) {
	a, srv, lib := flowApp(t)
	if post(t, srv.URL+"/api/pick-folder", `{}`) != 501 || post(t, srv.URL+"/api/import", `{}`) != 501 {
		t.Error("without the window there are no dialogs: 501")
	}
	a.pick = func(string) (string, error) { return "/somewhere/Manga", nil }
	var picked struct{ Folder string }
	resp, _ := http.Post(srv.URL+"/api/pick-folder", "application/json", strings.NewReader(`{}`))
	json.NewDecoder(resp.Body).Decode(&picked)
	resp.Body.Close()
	if picked.Folder != "/somewhere/Manga" {
		t.Errorf("pick-folder: %+v", picked)
	}
	a.pickBooks = func() ([]string, error) { return nil, nil } // she cancelled
	var started struct{ Started bool }
	resp, _ = http.Post(srv.URL+"/api/import", "application/json", strings.NewReader(`{}`))
	json.NewDecoder(resp.Body).Decode(&started)
	resp.Body.Close()
	if started.Started {
		t.Error("an import started with nothing picked")
	}
	var opened []string
	a.open = func(p string) error { opened = append(opened, p); return nil }
	if post(t, srv.URL+"/api/open", `{}`) != 204 {
		t.Fatal("opening the bookcase failed")
	}
	if len(opened) != 1 || opened[0] != lib {
		t.Errorf("opened %v, want the bookcase %s", opened, lib)
	}
	if _, err := os.Stat(lib); err != nil {
		t.Error("opening the bookcase should make it if it isn't there yet")
	}
}

func itoa(n int) string     { b, _ := json.Marshal(n); return string(b) }
func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
