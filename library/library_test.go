// SPDX-License-Identifier: EUPL-1.2

package library

import (
	"archive/zip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// cbz writes a .cbz with the given entries (name → content).
func cbz(t *testing.T, path string, entries ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, name := range entries {
		w, _ := z.Create(name)
		w.Write([]byte("page:" + name))
	}
	z.Close()
	f.Close()
}

func shelf(t *testing.T) Library {
	t.Helper()
	l := Library{Dir: t.TempDir()}
	// A series from MangaDex: manga.json, cover, two volumes out of order.
	if err := l.WriteInfo("Uso-douri no Kottouya", Info{MangaDex: "cbe84ea7-2992-4db0-b4ef-d042124f48c8", Title: "Uso-douri no Kottouya", Added: time.Unix(2000, 0)}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(l.Dir, "Uso-douri no Kottouya")
	os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte("jpg"), 0o644)
	cbz(t, filepath.Join(dir, "Vol. 10.cbz"), "ComicInfo.xml", "10.jpg", "2.jpg", "1.jpg")
	cbz(t, filepath.Join(dir, "Vol. 2.cbz"), "00.png")
	os.WriteFile(filepath.Join(dir, "download.db"), []byte("x"), 0o644)
	// One she copied in by hand: no manga.json, a PDF.
	os.Mkdir(filepath.Join(l.Dir, "Old Favourite"), 0o755)
	os.WriteFile(filepath.Join(l.Dir, "Old Favourite", "Book 1.pdf"), []byte("%PDF"), 0o644)
	os.Chtimes(filepath.Join(l.Dir, "Old Favourite"), time.Unix(1000, 0), time.Unix(1000, 0))
	// Not series: an empty folder, a hidden one, a loose file.
	os.Mkdir(filepath.Join(l.Dir, "empty"), 0o755)
	os.Mkdir(filepath.Join(l.Dir, ".trash"), 0o755)
	os.WriteFile(filepath.Join(l.Dir, "notes.txt"), nil, 0o644)
	return l
}

func TestScan(t *testing.T) {
	l := shelf(t)
	series, err := l.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 {
		t.Fatalf("got %d series: %+v", len(series), series)
	}
	uso, old := series[0], series[1] // newest added first
	if uso.MangaDex == "" || !uso.HasCover || uso.Title != "Uso-douri no Kottouya" {
		t.Errorf("uso: %+v", uso)
	}
	if len(uso.Books) != 2 || uso.Books[0].Name != "Vol. 2" || uso.Books[1].Name != "Vol. 10" || !uso.Books[0].Readable() {
		t.Errorf("books out of order: %+v", uso.Books)
	}
	if old.Title != "Old Favourite" || old.HasCover || old.Books[0].Format != "pdf" || old.Books[0].Readable() {
		t.Errorf("old: %+v", old)
	}
}

func TestScanMissingFolderIsAnEmptyShelf(t *testing.T) {
	series, err := Library{Dir: filepath.Join(t.TempDir(), "nope")}.Scan()
	if err != nil || len(series) != 0 || series == nil {
		t.Errorf("got %v, %v", series, err)
	}
}

func TestPagesInReadingOrder(t *testing.T) {
	l := shelf(t)
	pages, err := l.Pages("Uso-douri no Kottouya", "Vol. 10.cbz")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(pages, ",") != "1.jpg,2.jpg,10.jpg" {
		t.Errorf("pages %v (ComicInfo.xml isn't a page)", pages)
	}
	b, typ, err := l.Page("Uso-douri no Kottouya", "Vol. 10.cbz", 2)
	if err != nil || string(b) != "page:10.jpg" || typ != "image/jpeg" {
		t.Errorf("page 3: %q %q %v", b, typ, err)
	}
	if _, _, err := l.Page("Uso-douri no Kottouya", "Vol. 10.cbz", 3); err == nil {
		t.Error("page past the end should fail")
	}
}

func TestNothingLeavesTheShelf(t *testing.T) {
	l := shelf(t)
	os.WriteFile(filepath.Join(filepath.Dir(l.Dir), "secret.cbz"), nil, 0o644)
	for _, try := range [][2]string{
		{"..", "secret.cbz"},
		{"Uso-douri no Kottouya", "../../secret.cbz"},
		{"Uso-douri no Kottouya/..", "Vol. 2.cbz"},
		{`Uso-douri no Kottouya\..`, "Vol. 2.cbz"},
		{"Old Favourite", "Book 1.pdf"}, // real, but not a .cbz
	} {
		if _, err := l.Pages(try[0], try[1]); err == nil {
			t.Errorf("Pages(%q, %q) should fail", try[0], try[1])
		}
	}
	if _, err := l.Cover(".."); err == nil {
		t.Error("Cover(..) should fail")
	}
	if _, err := l.BookPath("Old Favourite", "../Uso-douri no Kottouya"); err == nil {
		t.Error("BookPath escaping should fail")
	}
	if err := l.WriteInfo("../x", Info{Title: "x"}); err == nil {
		t.Error("WriteInfo outside should fail")
	}
}

func TestFolderName(t *testing.T) {
	for in, want := range map[string]string{
		`Re:Zero / Season 2?`:             "Re-Zero - Season 2",
		"Uso-douri no Kottouya":           "Uso-douri no Kottouya",
		"  dots...  ":                     "dots",
		"CON":                             "CON manga",
		"???":                             "manga",
		"tab\there":                       "tabhere",
		"葬送のフリーレン":                        "葬送のフリーレン",
		strings.Repeat("long ", 30) + "!": strings.TrimSpace(strings.Repeat("long ", 16)),
	} {
		if got := FolderName(in); got != want {
			t.Errorf("FolderName(%q) = %q, want %q", in, got, want)
		}
		if got := FolderName(in); !ValidName(got) {
			t.Errorf("FolderName(%q) = %q isn't a valid name", in, got)
		}
	}
}

func TestLess(t *testing.T) {
	got := []string{"Vol. 10", "Vol. 2", "vol. 1", "Vol. 2 Ch. 11", "Vol. 2 Ch. 3", "Extra", "Vol. 01"}
	sort.Slice(got, func(i, j int) bool { return Less(got[i], got[j]) })
	want := "Extra,vol. 1,Vol. 01,Vol. 2,Vol. 2 Ch. 3,Vol. 2 Ch. 11,Vol. 10"
	if strings.Join(got, ",") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(got, ","), want)
	}
}
