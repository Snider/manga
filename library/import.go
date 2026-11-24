// SPDX-License-Identifier: EUPL-1.2

package library

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Imported is what one import did, for the page to tell her.
type Imported struct {
	Added   []string `json:"added"`   // "Berserk/Berserk v01.cbz"
	Same    []string `json:"same"`    // already on the shelf: left alone
	Skipped []string `json:"skipped"` // not a book ("notes.txt")
	Failed  []string `json:"failed"`  // "Berserk v02.cbz: disk full"
	Series  []string `json:"series"`  // the series folders it touched
}

// Import copies books onto the shelf: dropped files, or folders of them.
// Nothing she dropped is moved or changed; copies go into the series folder.
//
//	file  Berserk v01.cbz        → Berserk/Berserk v01.cbz (series from ComicInfo.xml, else the name)
//	folder Berserk/              → Berserk/…every book in it
//	folder Manga/ (of series)    → each sub-folder is a series
//
// A book already there (same name, same size) is left alone; a different
// one with the same name is copied as "name (2).cbz".
func (l Library) Import(paths []string) Imported {
	var out Imported
	touched := map[string]bool{}
	add := func(series, src string) {
		folder := FolderName(series)
		dst, state, err := l.copyBook(folder, src)
		switch {
		case err != nil:
			out.Failed = append(out.Failed, filepath.Base(src)+": "+err.Error())
		case state == "same":
			out.Same = append(out.Same, folder+"/"+filepath.Base(src))
		default:
			out.Added = append(out.Added, folder+"/"+dst)
			touched[folder] = true
		}
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			out.Failed = append(out.Failed, filepath.Base(p)+": can't read it")
			continue
		}
		if !st.IsDir() {
			if !IsBook(p) {
				out.Skipped = append(out.Skipped, filepath.Base(p))
				continue
			}
			add(SeriesOf(p), p)
			continue
		}
		books, subdirs := listDir(p)
		for _, b := range books { // a series folder
			add(filepath.Base(p), b)
		}
		for _, d := range subdirs { // a folder of series folders (one level down)
			inner, _ := listDir(d)
			for _, b := range inner {
				add(filepath.Base(d), b)
			}
		}
		if len(books) == 0 && len(subdirs) == 0 {
			out.Skipped = append(out.Skipped, filepath.Base(p)+" (no books in it)")
		}
	}
	for folder := range touched {
		out.Series = append(out.Series, folder)
		l.ensureCover(folder)
	}
	sort.Strings(out.Series)
	return out
}

// IsBook: a file the shelf shows (.cbz, .pdf, .epub, .cb7).
func IsBook(path string) bool { return formats[strings.ToLower(filepath.Ext(path))] }

var volumeTail = regexp.MustCompile(`(?i)[\s._-]*[\(\[]?(?:\b(?:v|vol|volume|c|ch|chap|chapter|t|tome|book)\.?|#)\s*\d+.*$`)

// SeriesOf names the series a dropped book belongs to: ComicInfo.xml's
// <Series> if it's a .cbz that has one, else the file name without its
// volume or chapter ("Berserk v01" → "Berserk", "One Piece - Vol. 3" → "One Piece").
func SeriesOf(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".cbz") {
		if s := comicInfoSeries(path); s != "" {
			return s
		}
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	name = strings.ReplaceAll(name, "_", " ")
	if s := strings.TrimSpace(volumeTail.ReplaceAllString(name, "")); s != "" {
		name = s
	}
	return strings.TrimRight(strings.TrimSpace(name), " -.")
}

func comicInfoSeries(path string) string {
	z, err := zip.OpenReader(path)
	if err != nil {
		return ""
	}
	defer z.Close()
	for _, f := range z.File {
		if !strings.EqualFold(filepath.Base(f.Name), "ComicInfo.xml") || f.UncompressedSize64 > 1<<20 {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return ""
		}
		defer r.Close()
		var info struct {
			Series string `xml:"Series"`
		}
		if xml.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&info) != nil {
			return ""
		}
		return strings.TrimSpace(info.Series)
	}
	return ""
}

// listDir: the books in a folder, and its sub-folders (not hidden ones).
func listDir(dir string) (books, subdirs []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			subdirs = append(subdirs, p)
		case IsBook(p):
			books = append(books, p)
		}
	}
	return books, subdirs
}

// copyBook copies src into the series folder: "same" if it's already there.
func (l Library) copyBook(folder, src string) (string, string, error) {
	if !ValidName(folder) {
		return "", "", fmt.Errorf("can't make a folder called %q", folder)
	}
	dir := filepath.Join(l.Dir, folder)
	from, _ := filepath.Abs(filepath.Dir(src))
	if to, _ := filepath.Abs(dir); from == to {
		return "", "same", nil // dropped from the shelf onto the shelf
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	st, err := os.Stat(src)
	if err != nil {
		return "", "", err
	}
	base := filepath.Base(src)
	ext := filepath.Ext(base)
	name := base
	for n := 2; ; n++ {
		existing, err := os.Stat(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err == nil && existing.Size() == st.Size() {
			return name, "same", nil
		}
		name = strings.TrimSuffix(base, ext) + " (" + strconv.Itoa(n) + ")" + ext
	}
	in, err := os.Open(src)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".importing-*")
	if err != nil {
		return "", "", err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", "", err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		os.Remove(tmp.Name())
		return "", "", err
	}
	return name, "added", nil
}

// ensureCover gives a series without a cover.jpg the first page of its first .cbz.
func (l Library) ensureCover(folder string) {
	s, err := l.Series(folder)
	if err != nil || s.HasCover {
		return
	}
	for _, b := range s.Books {
		if !b.Readable() {
			continue
		}
		page, _, err := l.Page(folder, b.File, 0)
		if err == nil && len(page) > 0 {
			os.WriteFile(filepath.Join(l.Dir, folder, "cover.jpg"), page, 0o644) // browsers sniff png/webp too
		}
		return
	}
}
