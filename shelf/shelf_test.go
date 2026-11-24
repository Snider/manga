// SPDX-License-Identifier: EUPL-1.2

package shelf

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) (*Shelf, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Pinku", "shelf.db")
	r := Open(path)
	if !r.OK {
		t.Fatal(r.Error())
	}
	s := r.Value.(*Shelf)
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestSettingsDefaultsThenSaved(t *testing.T) {
	s, path := open(t)
	v := s.Settings()
	if v.Library != DefaultLibrary() || v.Language != "en" || v.Format != "cbz-volume" {
		t.Errorf("defaults %+v", v)
	}
	v.Library = filepath.Join(t.TempDir(), "Manga")
	v.Language = "pt-br"
	v.Format = "cbz"
	if r := s.Save(v); !r.OK {
		t.Fatal(r.Error())
	}
	s.Close()
	again := Open(path).Value.(*Shelf) // survives a restart
	defer again.Close()
	if got := again.Settings(); got != v {
		t.Errorf("after reopening: %+v, want %+v", got, v)
	}
}

func TestSaveRefusesNonsense(t *testing.T) {
	s, _ := open(t)
	good := s.Settings()
	for _, bad := range []Settings{
		{Library: "Manga", Language: "en", Format: "cbz"},      // not a full path
		{Library: good.Library, Language: "en", Format: "pdf"}, // not offered
		{Library: good.Library, Language: "", Format: "cbz"},   // no language
	} {
		if r := s.Save(bad); r.OK {
			t.Errorf("Save(%+v) should fail", bad)
		}
	}
	if got := s.Settings(); got != good {
		t.Errorf("a refused save changed things: %+v", got)
	}
}

func TestProgress(t *testing.T) {
	s, _ := open(t)
	if len(s.Progress("Uso")) != 0 {
		t.Error("nothing read yet")
	}
	s.SetPage("Uso", "Vol. 1.cbz", 3, 40)
	s.SetPage("Uso", "Vol. 1.cbz", 39, 40)
	s.SetPage("Uso", "Vol. 2.cbz", 0, 38)
	if r := s.SetPage("Uso", "Vol. 3.cbz", 40, 40); r.OK {
		t.Error("page past the end should be refused")
	}
	p := s.Progress("Uso")
	if len(p) != 2 || !p["Vol. 1.cbz"].Done() || p["Vol. 2.cbz"].Done() || p["Vol. 2.cbz"].Pages != 38 {
		t.Errorf("progress %+v", p)
	}
	if len(s.Progress("Other")) != 0 {
		t.Error("series are kept apart")
	}
}

func TestChecked(t *testing.T) {
	s, _ := open(t)
	if !s.LastChecked("Uso").IsZero() {
		t.Error("never checked")
	}
	at := time.Unix(1_800_000_000, 0)
	s.Checked("Uso", at)
	if !s.LastChecked("Uso").Equal(at) {
		t.Errorf("got %v", s.LastChecked("Uso"))
	}
}

func TestMovedFrom(t *testing.T) {
	d := t.TempDir()
	old, dir := filepath.Join(d, "MangaShelf"), filepath.Join(d, "Pinku")
	if got := movedFrom(old, dir); got != dir {
		t.Errorf("nothing there yet: got %q", got)
	}
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "shelf.db"), []byte("hers"), 0o644)
	if got := movedFrom(old, dir); got != dir {
		t.Fatalf("got %q, want the new folder", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "shelf.db")); string(b) != "hers" {
		t.Errorf("the store didn't come along: %q", b)
	}
	os.MkdirAll(old, 0o755) // both exist: the new one wins, the old is left alone
	if got := movedFrom(old, dir); got != dir {
		t.Errorf("both: got %q", got)
	}
}
