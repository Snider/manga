// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

// The zips mangadex-dl comes in, and what goes wrong with them: folder
// entries (real releases have them) unpack; not-a-zip, a corrupt entry, or
// nowhere to unpack it fail without installing anything.
func TestInstallToolZips(t *testing.T) {
	withDirs := zipWithDirs(t)
	if _, err := installTool(withDirs, shaOf(withDirs), filepath.Join(t.TempDir(), "tools")); err != nil {
		t.Errorf("a zip with folder entries: %v", err)
	}
	junk := []byte("not a zip at all")
	if _, err := installTool(junk, shaOf(junk), filepath.Join(t.TempDir(), "tools")); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("not a zip: %v", err)
	}
	good, sha := zipOf(t, map[string]string{"mangadex-dl/mangadex-dl.exe": strings.Repeat("MZ", 4096)})
	file := filepath.Join(t.TempDir(), "tools")
	os.WriteFile(file, []byte("a file where the folder goes"), 0o644)
	if _, err := installTool(good, sha, file); err == nil {
		t.Error("unpacked into a file")
	}
	bad := corruptFirstEntry(good)
	if _, err := installTool(bad, shaOf(bad), filepath.Join(t.TempDir(), "tools")); err == nil {
		t.Error("a corrupt entry installed")
	}
}

// A download that stops half way is an error, not half a tool.
func TestFetchToolCutOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.Write([]byte("PK just the start"))
		if h, ok := w.(http.Hijacker); ok {
			conn, _, _ := h.Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()
	if _, err := fetchTool(context.Background(), srv.URL, "x", t.TempDir(), func(string) {}); err == nil {
		t.Error("a cut-off download installed")
	}
}

// pip's mangadex-dl, on the PATH, is found when there's none beside Pinku.
func TestFindToolOnPath(t *testing.T) {
	bin := t.TempDir()
	name := "mangadex-dl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin)
	if got := FindTool("", t.TempDir()); got != filepath.Join(bin, name) {
		t.Errorf("FindTool = %q, want the one on PATH", got)
	}
}

func shaOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func zipWithDirs(t *testing.T) []byte {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	z.Create("mangadex-dl/")
	z.Create("mangadex-dl/_internal/")
	w, _ := z.Create("mangadex-dl/mangadex-dl.exe")
	w.Write([]byte("MZ"))
	w, _ = z.Create("mangadex-dl/_internal/python.dll")
	w.Write([]byte("dll"))
	z.Close()
	return buf.Bytes()
}

// corruptFirstEntry flips bytes in the first entry's compressed data, so
// reading it fails its checksum.
func corruptFirstEntry(b []byte) []byte {
	out := append([]byte(nil), b...)
	nameLen := int(out[26]) | int(out[27])<<8
	extraLen := int(out[28]) | int(out[29])<<8
	start := 30 + nameLen + extraLen
	for i := start; i < start+16 && i < len(out); i++ {
		out[i] ^= 0xff
	}
	return out
}
