// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"manga/library"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"manga/internal/fakedl"
	"manga/mangadex"
	"manga/shelf"
)

// testTerminal is the commands over a temp shelf (bookcase in it), the fake
// MangaDex and the fake mangadex-dl. Never her real settings.
func testTerminal(t *testing.T) (terminal, *strings.Builder, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "shelf.db")
	lib := filepath.Join(dir, "Manga")
	r := shelf.Open(path)
	if !r.OK {
		t.Fatal(r.Error())
	}
	sh := r.Value.(*shelf.Shelf)
	set := sh.Settings()
	set.Library, set.Tool = lib, fakedl.Tool(t)
	if r := sh.Save(set); !r.OK {
		t.Fatal(r.Error())
	}
	sh.Close()
	dex := fakeDex(t)
	c := mangadex.New()
	c.BaseURL, c.UploadsURL = dex.URL, dex.URL
	var out strings.Builder
	return terminal{out: &out, dex: c, shelfPath: path}, &out, lib
}

func runCommand(tm terminal, args ...string) bool {
	return tm.commands().Cli().Run(args...).OK
}

// search → get → shelf → update, as she'd type them.
func TestCommandsEndToEnd(t *testing.T) {
	tm, out, _ := testTerminal(t)
	if !runCommand(tm, "search", "uso", "douri") || !strings.Contains(out.String(), "Uso-douri") || !strings.Contains(out.String(), usoID) {
		t.Fatalf("search printed:\n%s", out)
	}
	out.Reset()
	if !runCommand(tm, "shelf") || !strings.Contains(out.String(), "(empty") {
		t.Fatalf("empty shelf printed:\n%s", out)
	}
	out.Reset()
	if !runCommand(tm, "get", "https://mangadex.org/title/"+usoID) || !strings.Contains(out.String(), "done") {
		t.Fatalf("get printed:\n%s", out)
	}
	out.Reset()
	if !runCommand(tm, "shelf") || !strings.Contains(out.String(), "1 books, 0 read") {
		t.Fatalf("shelf printed:\n%s", out)
	}
	out.Reset()
	if !runCommand(tm, "update") || !strings.Contains(out.String(), "done") {
		t.Fatalf("update printed:\n%s", out)
	}
}

// Commands that can't do anything say why, and fail (a non-zero exit).
func TestCommandsRefuse(t *testing.T) {
	tm, out, _ := testTerminal(t)
	for _, args := range [][]string{{"search"}, {"get"}, {"get", "not-a-link"}, {"update", "no such series"}} {
		if runCommand(tm, args...) {
			t.Errorf("%v succeeded", args)
		}
	}
	out.Reset()
	if !runCommand(tm, "update") || !strings.Contains(out.String(), "nothing from MangaDex") {
		t.Errorf("update with an empty shelf:\n%s", out)
	}
}

// Words that aren't commands open the window instead (flags, or nothing).
func TestIsCommand(t *testing.T) {
	c := terminal{out: &strings.Builder{}}.commands()
	for args, want := range map[string]bool{"search frieren": true, "shelf": true, "--replay x": false, "": false, "frieren": false} {
		if got := isCommand(c, strings.Fields(args)); got != want {
			t.Errorf("isCommand(%q) = %v", args, got)
		}
	}
}

// MangaDex down, nothing found, a download that fails, and reading counts.
func TestCommandsWhenThingsGoWrong(t *testing.T) {
	tm, out, lib := testTerminal(t)
	tm.dex.BaseURL = "http://127.0.0.1:1"
	if runCommand(tm, "search", "uso") || runCommand(tm, "get", usoID) {
		t.Error("MangaDex unreachable should fail the command")
	}

	// A series whose download fails (the fake tool fails folders called FAIL…).
	(library.Library{Dir: lib}).WriteInfo("FAIL here", library.Info{MangaDex: usoID, Title: "FAIL here", Added: time.Now()})
	out.Reset()
	if runCommand(tm, "update", "FAIL here") || !strings.Contains(out.String(), "no chapters") {
		t.Errorf("a failed download should fail the command and say why:\n%s", out)
	}

	// Reading progress shows in shelf.
	(library.Library{Dir: lib}).WriteInfo("Read One", library.Info{MangaDex: usoID, Title: "Read One", Added: time.Now()})
	os.WriteFile(filepath.Join(lib, "Read One", "Vol. 1.cbz"), []byte("x"), 0o644)
	r := shelf.Open(tm.shelfPath)
	sh := r.Value.(*shelf.Shelf)
	sh.SetPage("Read One", "Vol. 1.cbz", 4, 5)
	sh.Close()
	out.Reset()
	if !runCommand(tm, "shelf") || !strings.Contains(out.String(), "1 books, 1 read") {
		t.Errorf("shelf:\n%s", out)
	}

	// No results.
	none := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[]}`)) }))
	defer none.Close()
	tm.dex.BaseURL = none.URL
	out.Reset()
	if !runCommand(tm, "search", "zzzz") || !strings.Contains(out.String(), "nothing on MangaDex") {
		t.Errorf("no results:\n%s", out)
	}

	// A shelf path that can't be opened.
	tm.shelfPath = filepath.Join(lib, "Read One", "Vol. 1.cbz", "shelf.db")
	if runCommand(tm, "shelf") {
		t.Error("shelf with an unopenable store")
	}
}

func TestYear(t *testing.T) {
	if year(0) != "" || year(2019) != ", 2019" {
		t.Errorf("year: %q %q", year(0), year(2019))
	}
}
