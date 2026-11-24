// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fetching mangadex-dl: downloaded, checked against its checksum, unpacked,
// and the status line tells her what's happening.
func TestFetchTool(t *testing.T) {
	body, sha := zipOf(t, map[string]string{"mangadex-dl/mangadex-dl.exe": "MZ"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	var said []string
	dataDir := t.TempDir()
	exe, err := fetchTool(context.Background(), srv.URL, sha, dataDir, func(s string) { said = append(said, s) })
	if err != nil {
		t.Fatal(err)
	}
	if exe != filepath.Join(dataDir, "tools", "mangadex-dl", "mangadex-dl.exe") || len(said) != 2 {
		t.Errorf("exe %s, said %v", exe, said)
	}
	if b, _ := os.ReadFile(exe); string(b) != "MZ" {
		t.Errorf("installed %q", b)
	}
}

// A download that's tampered with, missing, or can't be reached installs
// nothing and says why.
func TestFetchToolRefuses(t *testing.T) {
	body, _ := zipOf(t, map[string]string{"mangadex-dl/mangadex-dl.exe": "MZ"})
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer ok.Close()
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer gone.Close()
	quiet := func(string) {}
	for name, c := range map[string]struct{ url, sha, want string }{
		"wrong checksum": {ok.URL, strings.Repeat("0", 64), "checksum"},
		"not found":      {gone.URL, "x", "GitHub answered"},
		"unreachable":    {"http://127.0.0.1:1/nope", "x", "couldn't download"},
		"not a url":      {"::", "x", ""},
	} {
		dataDir := t.TempDir()
		_, err := fetchTool(context.Background(), c.url, c.sha, dataDir, quiet)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if _, statErr := os.Stat(filepath.Join(dataDir, "tools", "mangadex-dl")); statErr == nil {
			t.Errorf("%s: something was installed", name)
		}
	}
}

// Off Windows there's nothing to fetch: it says how to install it instead.
func TestFetchToolElsewhere(t *testing.T) {
	if CanFetch() {
		t.Skip("this computer can fetch it")
	}
	if _, err := FetchTool(context.Background(), t.TempDir(), func(string) {}); err == nil || !strings.Contains(err.Error(), "pip install") {
		t.Errorf("err = %v", err)
	}
}

// A Tool setting that points nowhere is ignored, and the search goes on.
func TestFindToolSettingGone(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if got := FindTool(filepath.Join(t.TempDir(), "moved-away.exe"), t.TempDir()); got != "" {
		t.Errorf("FindTool = %q", got)
	}
	real := filepath.Join(t.TempDir(), "mangadex-dl")
	os.WriteFile(real, []byte("x"), 0o755)
	if got := FindTool(real, t.TempDir()); got != real {
		t.Errorf("an existing setting = %q", got)
	}
}
