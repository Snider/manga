// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/dappcore/go"
	process "github.com/dappcore/go-process/go"

	"manga/downloader"
	"manga/library"
	"manga/mangadex"
	"manga/shelf"
)

// testApp is the page's API over a temp shelf with one series in it.
func testApp(t *testing.T, libDir string) (*app, *httptest.Server) {
	t.Helper()
	r := shelf.Open(filepath.Join(t.TempDir(), "shelf.db"))
	if !r.OK {
		t.Fatal(r.Error())
	}
	sh := r.Value.(*shelf.Shelf)
	t.Cleanup(func() { sh.Close() })
	set := sh.Settings()
	set.Library = libDir
	if r := sh.Save(set); !r.OK {
		t.Fatal(r.Error())
	}
	c := core.New(
		core.WithService(process.Register),
		core.WithService(downloader.Register(downloader.Options{DataDir: t.TempDir(), Settings: sh.Settings})),
		core.WithServiceLock(),
	)
	if r := c.ServiceStartup(core.Background(), nil); !r.OK {
		t.Fatal(r.Error())
	}
	t.Cleanup(func() { c.ServiceShutdown(core.Background()) })
	page, _ := fs.Sub(frontend, "frontend")
	var opened, browsed []string
	a := &app{
		shelf: sh, dex: mangadex.New(), down: core.MustServiceFor[*downloader.Service](c, "downloader"),
		assets: http.FileServerFS(page),
		open:   func(p string) error { opened = append(opened, p); return nil },
		browse: func(u string) error { browsed = append(browsed, u); return nil },
	}
	srv := httptest.NewServer(a.handler())
	t.Cleanup(srv.Close)
	return a, srv
}

func seed(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	lib := library.Library{Dir: dir}
	lib.WriteInfo("Uso", library.Info{MangaDex: "cbe84ea7-2992-4db0-b4ef-d042124f48c8", Title: "Uso-douri no Kottouya", Added: time.Now()})
	os.WriteFile(filepath.Join(dir, "Uso", "cover.jpg"), []byte("jpg"), 0o644)
	f, _ := os.Create(filepath.Join(dir, "Uso", "Vol. 1.cbz"))
	z := zip.NewWriter(f)
	for _, n := range []string{"01.jpg", "00.jpg", "ComicInfo.xml"} {
		w, _ := z.Create(n)
		w.Write([]byte(n))
	}
	z.Close()
	f.Close()
	return dir
}

func fetch(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil {
		if s, ok := out.(*string); ok {
			*s = string(b)
		} else {
			json.Unmarshal(b, out)
		}
	}
	return resp.StatusCode
}

func post(t *testing.T, url, body string) int {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestShelfReadAndRemember(t *testing.T) {
	_, srv := testApp(t, seed(t))
	var sh struct {
		Series []struct {
			Folder, Title string
			Books         []library.Book
			Read          int
		}
	}
	if fetch(t, srv.URL+"/api/shelf", &sh) != 200 || len(sh.Series) != 1 || sh.Series[0].Title != "Uso-douri no Kottouya" {
		t.Fatalf("shelf: %+v", sh)
	}
	var book struct{ Pages, Page int }
	fetch(t, srv.URL+"/api/book/Uso/Vol.%201.cbz", &book)
	if book.Pages != 2 || book.Page != 0 {
		t.Errorf("book: %+v", book)
	}
	var page string
	if fetch(t, srv.URL+"/api/page/Uso/Vol.%201.cbz/1", &page) != 200 || page != "01.jpg" {
		t.Errorf("page 2 is %q", page)
	}
	if post(t, srv.URL+"/api/progress", `{"folder":"Uso","file":"Vol. 1.cbz","page":1,"pages":2}`) != 204 {
		t.Error("progress not saved")
	}
	fetch(t, srv.URL+"/api/book/Uso/Vol.%201.cbz", &book)
	fetch(t, srv.URL+"/api/shelf", &sh)
	if book.Page != 1 || sh.Series[0].Read != 1 {
		t.Errorf("after reading to the end: book %+v, read %d", book, sh.Series[0].Read)
	}
	var cover string
	if fetch(t, srv.URL+"/api/cover/Uso", &cover) != 200 || cover != "jpg" {
		t.Errorf("cover %q", cover)
	}
	var index string
	if fetch(t, srv.URL+"/", &index) != 200 || !strings.Contains(index, "Manga Shelf") {
		t.Error("the page itself isn't served")
	}
}

func TestAPIRefusesNonsense(t *testing.T) {
	a, srv := testApp(t, seed(t))
	for _, path := range []string{"/api/cover/..", "/api/page/Uso/..%2F..%2Fx.cbz/0", "/api/page/Uso/Vol.%201.cbz/9", "/api/book/..%2F/x"} {
		if code := fetch(t, srv.URL+path, nil); code == 200 {
			t.Errorf("GET %s = 200", path)
		}
	}
	if post(t, srv.URL+"/api/browse", `{"url":"file:///etc/passwd"}`) != 400 || post(t, srv.URL+"/api/browse", `{"url":"https://mangadex.org"}`) != 204 {
		t.Error("browse should only open https links")
	}
	if post(t, srv.URL+"/api/settings", `{"library":"relative","language":"en","format":"cbz"}`) != 400 {
		t.Error("a relative bookcase folder should be refused")
	}
	if post(t, srv.URL+"/api/add", `{"id":"nope","title":"x"}`) != 400 {
		t.Error("add without a MangaDex id should be refused")
	}
	if post(t, srv.URL+"/api/progress", `{"folder":"Uso","file":"Vol. 1.cbz","page":5,"pages":2}`) != 400 {
		t.Error("a page past the end should be refused")
	}
	if post(t, srv.URL+"/api/open", `{"folder":"Uso","file":"../../etc"}`) != 400 {
		t.Error("open outside the shelf should be refused")
	}
	_ = a
}

func TestImportThroughTheAPI(t *testing.T) {
	lib := seed(t)
	a, srv := testApp(t, lib)
	src := filepath.Join(t.TempDir(), "Berserk v01.cbz")
	f, _ := os.Create(src)
	z := zip.NewWriter(f)
	w, _ := z.Create("001.jpg")
	w.Write([]byte("page"))
	z.Close()
	f.Close()
	if !a.Import([]string{src}) {
		t.Fatal("import didn't start")
	}
	var st importState
	for i := 0; i < 100; i++ {
		fetch(t, srv.URL+"/api/imports", &st)
		if !st.Busy && st.N == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.N != 1 || len(st.Result.Added) != 1 || st.Result.Series[0] != "Berserk" {
		t.Fatalf("import: %+v", st)
	}
	var sh struct{ Series []struct{ Folder string } }
	fetch(t, srv.URL+"/api/shelf", &sh)
	if len(sh.Series) != 2 {
		t.Errorf("shelf after import: %+v", sh)
	}
	if a.Import(nil) {
		t.Error("importing nothing shouldn't start")
	}
}

// TestPreview serves the page with a real shelf for a look in a browser:
//
//	MANGASHELF_PREVIEW=~/Documents/Manga go test ./cmd/mangashelf -run Preview -timeout 0
//
// then open http://127.0.0.1:8765. A dev tool: the program itself never opens a port.
func TestPreview(t *testing.T) {
	dir := os.Getenv("MANGASHELF_PREVIEW")
	if dir == "" {
		t.Skip("MANGASHELF_PREVIEW not set")
	}
	a, _ := testApp(t, dir)
	l, err := net.Listen("tcp", "127.0.0.1:8765")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("http://127.0.0.1:8765")
	http.Serve(l, a.handler())
}
