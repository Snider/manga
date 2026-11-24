// SPDX-License-Identifier: EUPL-1.2

package library

import (
	"archive/zip"
	"os"
	"path/filepath"
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
