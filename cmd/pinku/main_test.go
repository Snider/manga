// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/dappcore/go"
)

// What main decides before any window: a command runs (and fails without
// touching the network when it's missing its words); anything else is the
// window's.
func TestRunTerminal(t *testing.T) {
	var out strings.Builder
	if handled, _ := runTerminal(nil, &out); handled {
		t.Error("no args is the window, not a command")
	}
	if handled, _ := runTerminal([]string{"--replay", "x"}, &out); handled {
		t.Error("a flag is the window's")
	}
	if handled, r := runTerminal([]string{"search"}, &out); !handled || r.OK {
		t.Errorf("search with nothing to look for: handled %v ok %v", handled, r.OK)
	}
}

// newApp over a real shelf serves the page and its API, as the window will.
func TestNewApp(t *testing.T) {
	sh, c, err := openShelf(filepath.Join(t.TempDir(), "shelf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sh.Close()
	defer c.ServiceShutdown(core.Background())
	a := newApp(sh, c)
	if a.words == nil || a.down == nil {
		t.Fatalf("app missing parts: %+v", a)
	}
	srv := httptest.NewServer(a.handler())
	defer srv.Close()
	var page string
	if fetch(t, srv.URL+"/", &page) != 200 || !strings.Contains(page, "Pinku") {
		t.Error("the page isn't served")
	}
	var jobs []any
	if fetch(t, srv.URL+"/api/jobs", &jobs) != 200 {
		t.Error("the API isn't served")
	}
}

// A store that can't be opened is an error main can report, not a panic.
func TestOpenShelfFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, []byte("x"), 0o644)
	if _, _, err := openShelf(filepath.Join(file, "shelf.db")); err == nil {
		t.Error("opened a store inside a file")
	}
}
