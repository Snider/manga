// SPDX-License-Identifier: EUPL-1.2

package library

import (
	"archive/zip"
	"errors"
	core "github.com/dappcore/go"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cbzWith writes a .cbz with a page and, if series isn't "", a ComicInfo.xml.
func cbzWith(t *testing.T, path, series string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	if series != "" {
		w, _ := z.Create("ComicInfo.xml")
		w.Write([]byte(`<?xml version="1.0"?><ComicInfo><Series>` + series + `</Series><Volume>1</Volume></ComicInfo>`))
	}
	w, _ := z.Create("001.jpg")
	w.Write([]byte("first page of " + filepath.Base(path)))
	z.Close()
	f.Close()
}

func TestSeriesOf(t *testing.T) {
	dir := t.TempDir()
	tagged := filepath.Join(dir, "whatever_v01.cbz")
	cbzWith(t, tagged, "Yotsuba&amp;!") // ComicInfo.xml escapes it
	for path, want := range map[string]string{
		tagged:                                  "Yotsuba&!",
		"/x/Berserk v01.cbz":                    "Berserk",
		"/x/Berserk_Vol.02.cbz":                 "Berserk",
		"/x/One Piece - Volume 3.pdf":           "One Piece",
		"/x/Chainsaw Man c097 (2020) [Viz].cbz": "Chainsaw Man",
		"/x/Kaiju No. 8 v01.cbz":                "Kaiju No. 8",
		"/x/Mob Psycho 100 v1.epub":             "Mob Psycho 100",
		"/x/Vinland Saga #12.cbz":               "Vinland Saga",
		"/x/v01.cbz":                            "v01",
		"/x/Frieren.cbz":                        "Frieren",
	} {
		if got := SeriesOf(path); got != want {
			t.Errorf("SeriesOf(%q) = %q, want %q", filepath.Base(path), got, want)
		}
	}
}

func TestImportFilesAndFolders(t *testing.T) {
	src := t.TempDir()
	lib := Library{Dir: filepath.Join(t.TempDir(), "Manga")}
	cbzWith(t, filepath.Join(src, "Berserk v01.cbz"), "")
	cbzWith(t, filepath.Join(src, "Berserk v02.cbz"), "")
	os.WriteFile(filepath.Join(src, "notes.txt"), []byte("hi"), 0o644)
	cbzWith(t, filepath.Join(src, "Old Manga", "Frieren", "Frieren 01.cbz"), "") // a folder of series
	cbzWith(t, filepath.Join(src, "Old Manga", "Yotsuba", "Yotsuba 01.cbz"), "")
	cbzWith(t, filepath.Join(src, "Dandadan", "chapter 1.cbz"), "") // a series folder

	got := lib.Import([]string{
		filepath.Join(src, "Berserk v01.cbz"), filepath.Join(src, "Berserk v02.cbz"),
		filepath.Join(src, "notes.txt"), filepath.Join(src, "Old Manga"), filepath.Join(src, "Dandadan"),
		filepath.Join(src, "missing.cbz"),
	})
	if len(got.Added) != 5 || strings.Join(got.Series, ",") != "Berserk,Dandadan,Frieren,Yotsuba" {
		t.Errorf("added %v, series %v", got.Added, got.Series)
	}
	if len(got.Skipped) != 1 || len(got.Failed) != 1 {
		t.Errorf("skipped %v, failed %v", got.Skipped, got.Failed)
	}
	series, _ := lib.Scan()
	if len(series) != 4 {
		t.Fatalf("on the shelf: %+v", series)
	}
	b, _ := lib.Series("Berserk")
	if len(b.Books) != 2 || !b.HasCover {
		t.Errorf("Berserk: %+v", b)
	}
	if cover, _ := lib.Cover("Berserk"); string(cover) != "first page of Berserk v01.cbz" {
		t.Errorf("cover is %q, want the first page of the first volume", cover)
	}
	if _, err := os.Stat(filepath.Join(src, "Berserk v01.cbz")); err != nil {
		t.Error("the original was moved: imports copy")
	}

	// Again: nothing new. A different file with a taken name gets "(2)".
	again := lib.Import([]string{filepath.Join(src, "Berserk v01.cbz")})
	if len(again.Added) != 0 || len(again.Same) != 1 {
		t.Errorf("re-import: %+v", again)
	}
	other := filepath.Join(t.TempDir(), "Berserk v01.cbz")
	cbzWith(t, other, "Berserk") // the ComicInfo.xml makes it a different size
	clash := lib.Import([]string{other})
	if len(clash.Added) != 1 || !strings.HasSuffix(clash.Added[0], "Berserk v01 (2).cbz") {
		t.Errorf("name clash: %+v", clash)
	}
	// Dropping a book that's already in its shelf folder does nothing.
	if self := lib.Import([]string{filepath.Join(lib.Dir, "Berserk", "Berserk v02.cbz")}); len(self.Added) != 0 {
		t.Errorf("shelf onto shelf: %+v", self)
	}
	leftovers, _ := filepath.Glob(filepath.Join(lib.Dir, "*", ".importing-*"))
	if len(leftovers) != 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}

// Imports that can't happen say why, and leave nothing half-done.
func TestImportTrouble(t *testing.T) {
	src := t.TempDir()
	cbzWith(t, filepath.Join(src, "Berserk v01.cbz"), "")
	os.Mkdir(filepath.Join(src, "Empty Folder"), 0o755)
	os.WriteFile(filepath.Join(src, "Empty Folder", "notes.txt"), []byte("x"), 0o644)

	// The bookcase "folder" is a file.
	file := filepath.Join(t.TempDir(), "Manga")
	os.WriteFile(file, []byte("x"), 0o644)
	if got := (Library{Dir: file}).Import([]string{filepath.Join(src, "Berserk v01.cbz")}); len(got.Failed) != 1 || len(got.Added) != 0 {
		t.Errorf("into a file: %+v", got)
	}

	// A folder with no books in it.
	lib := Library{Dir: filepath.Join(t.TempDir(), "Manga")}
	if got := lib.Import([]string{filepath.Join(src, "Empty Folder")}); len(got.Skipped) != 1 || !strings.Contains(got.Skipped[0], "no books") {
		t.Errorf("empty folder: %+v", got)
	}

	// ComicInfo.xml that isn't XML: the file name decides.
	bad := filepath.Join(src, "Vagabond v03.cbz")
	f, _ := os.Create(bad)
	z := zip.NewWriter(f)
	w, _ := z.Create("ComicInfo.xml")
	w.Write([]byte("<ComicInfo><Series>unclosed"))
	z.Close()
	f.Close()
	if got := SeriesOf(bad); got != "Vagabond" {
		t.Errorf("SeriesOf with broken ComicInfo = %q", got)
	}
	// A .cbz that isn't a zip at all: the name decides too.
	notZip := filepath.Join(src, "Monster v01.cbz")
	os.WriteFile(notZip, []byte("nope"), 0o644)
	if got := SeriesOf(notZip); got != "Monster" {
		t.Errorf("SeriesOf a broken .cbz = %q", got)
	}
}

// Copies that can't happen are reported per book, and the rest carry on:
// a series folder name taken by a file, an unreadable source, hidden files
// in a dropped folder (skipped), and a series whose first book is a PDF
// (the cover comes from its .cbz).
func TestImportPerBookTrouble(t *testing.T) {
	src := t.TempDir()
	cbzWith(t, filepath.Join(src, "Berserk v01.cbz"), "")
	cbzWith(t, filepath.Join(src, "Vagabond v01.cbz"), "")
	lib := Library{Dir: filepath.Join(t.TempDir(), "Manga")}
	os.MkdirAll(lib.Dir, 0o755)
	os.WriteFile(filepath.Join(lib.Dir, "Berserk"), []byte("a file, not a folder"), 0o644)
	got := lib.Import([]string{filepath.Join(src, "Berserk v01.cbz"), filepath.Join(src, "Vagabond v01.cbz")})
	if len(got.Failed) != 1 || !strings.HasPrefix(got.Failed[0], "Berserk v01.cbz") || len(got.Added) != 1 {
		t.Errorf("one bad, one good: %+v", got)
	}

	folder := filepath.Join(src, "Pluto")
	os.Mkdir(folder, 0o755)
	os.WriteFile(filepath.Join(folder, "A Pluto.pdf"), []byte("%PDF"), 0o644)
	cbzWith(t, filepath.Join(folder, "B Pluto v01.cbz"), "")
	cbzWith(t, filepath.Join(folder, ".hidden v99.cbz"), "")
	got = lib.Import([]string{folder})
	if len(got.Added) != 2 {
		t.Errorf("hidden file imported, or books missed: %+v", got)
	}
	if s, _ := lib.Series("Pluto"); !s.HasCover {
		t.Error("no cover from the .cbz after a PDF")
	}

	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		locked := filepath.Join(src, "Locked v01.cbz")
		cbzWith(t, locked, "")
		os.Chmod(locked, 0)
		defer os.Chmod(locked, 0o644)
		if got := lib.Import([]string{locked}); len(got.Failed) != 1 {
			t.Errorf("an unreadable book: %+v", got)
		}
	}
}

// Save writes a downloaded book in place only when it's whole, and never
// over one that's there.
func TestSave(t *testing.T) {
	lib := Library{Dir: filepath.Join(t.TempDir(), "Manga")}
	write := func(s string) func(core.Writer) error {
		return func(w core.Writer) error { core.WriteString(w, s); return nil }
	}
	name, err := lib.Save("Frieren", "Frieren v01.cbz", write("one"))
	if err != nil || name != "Frieren v01.cbz" {
		t.Fatalf("save: %q %v", name, err)
	}
	if name, _ := lib.Save("Frieren", "Frieren v01.cbz", write("another")); name != "Frieren v01 (2).cbz" {
		t.Errorf("a taken name: %q", name)
	}
	if _, err := lib.Save("Frieren", "Frieren v02.cbz", func(core.Writer) error { return errors.New("cut off") }); err == nil {
		t.Error("a failed download was saved")
	}
	leftovers, _ := filepath.Glob(filepath.Join(lib.Dir, "Frieren", ".saving-*"))
	if s, _ := lib.Series("Frieren"); len(s.Books) != 2 || len(leftovers) != 0 {
		t.Errorf("books %+v, leftovers %v", s.Books, leftovers)
	}
	for _, bad := range [][2]string{{"..", "x.cbz"}, {"Frieren", "../x.cbz"}, {"Frieren", ".hidden.cbz"}} {
		if _, err := lib.Save(bad[0], bad[1], write("x")); err == nil {
			t.Errorf("saved %v", bad)
		}
	}
}
