// SPDX-License-Identifier: EUPL-1.2

// Package library is the bookcase: a folder with one folder per series, each
// holding its books (.cbz, .pdf, .epub, .cb7) and a cover.jpg. That's also
// what mangadex-dl writes, so whatever it downloads just appears.
//
//	Manga/
//	  Uso-douri no Kottouya/
//	    cover.jpg
//	    manga.json      ← ours: where it came from (so "check for new" works)
//	    Vol. 1.cbz
//	    Vol. 2.cbz
//
// Anything else in the folder (series she already had, copied in by hand)
// shows up too, without a manga.json.
//
//	lib := library.Library{Dir: `C:\Users\her\Documents\Manga`}
//	series, err := lib.Scan()
//	pages, err := lib.Pages(series[0].Folder, series[0].Books[0].File)
package library

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Library is the bookcase folder.
type Library struct {
	Dir string
}

// Series is one folder on the shelf.
type Series struct {
	Folder   string `json:"folder"` // the folder's name, which is also its id here
	Title    string `json:"title"`
	MangaDex string `json:"mangadex,omitempty"` // the MangaDex id, if it was downloaded from there
	HasCover bool   `json:"hasCover"`
	Books    []Book `json:"books"`
	Added    int64  `json:"added"` // unix seconds
}

// Book is one file in a series.
type Book struct {
	File   string `json:"file"`
	Name   string `json:"name"`   // "Vol. 1" (the file name without its extension)
	Format string `json:"format"` // cbz, pdf, epub, cb7
	Size   int64  `json:"size"`
}

// Readable: can the built-in reader open it? (Other formats open in her own app.)
func (b Book) Readable() bool { return b.Format == "cbz" }

// Info is manga.json, written when a series is added from MangaDex.
type Info struct {
	MangaDex string    `json:"mangadex"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Language string    `json:"language"`
	Format   string    `json:"format"` // the mangadex-dl --save-as it was downloaded with
	Added    time.Time `json:"added"`
}

var formats = map[string]bool{".cbz": true, ".pdf": true, ".epub": true, ".cb7": true}

// Scan lists every series, newest-added first. A missing folder is an empty shelf.
func (l Library) Scan() ([]Series, error) {
	entries, err := os.ReadDir(l.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Series{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Series{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		s, err := l.Series(e.Name())
		if err != nil || (len(s.Books) == 0 && s.MangaDex == "") {
			continue // not a series (or one still being started)
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Added > out[j].Added })
	return out, nil
}

// Series reads one series folder.
func (l Library) Series(folder string) (Series, error) {
	root, err := l.open(folder)
	if err != nil {
		return Series{}, err
	}
	defer root.Close()
	s := Series{Folder: folder, Title: folder, Books: []Book{}}
	if st, err := root.Stat("."); err == nil {
		s.Added = st.ModTime().Unix()
	}
	if info, err := readInfo(root); err == nil {
		s.MangaDex, s.Title, s.Added = info.MangaDex, info.Title, info.Added.Unix()
	}
	_, err = root.Stat("cover.jpg")
	s.HasCover = err == nil
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return s, err
	}
	for _, e := range entries {
		ext := strings.ToLower(path.Ext(e.Name()))
		if e.IsDir() || !formats[ext] {
			continue
		}
		size := int64(0)
		if fi, err := e.Info(); err == nil {
			size = fi.Size()
		}
		s.Books = append(s.Books, Book{File: e.Name(), Name: strings.TrimSuffix(e.Name(), path.Ext(e.Name())), Format: ext[1:], Size: size})
	}
	sort.Slice(s.Books, func(i, j int) bool { return Less(s.Books[i].Name, s.Books[j].Name) })
	return s, nil
}

// Info reads a series' manga.json (an error if it has none).
func (l Library) Info(folder string) (Info, error) {
	root, err := l.open(folder)
	if err != nil {
		return Info{}, err
	}
	defer root.Close()
	return readInfo(root)
}

// WriteInfo creates the series folder (if need be) and its manga.json.
func (l Library) WriteInfo(folder string, info Info) error {
	if !ValidName(folder) {
		return fmt.Errorf("bad folder name %q", folder)
	}
	if err := os.MkdirAll(filepath.Join(l.Dir, folder), 0o755); err != nil {
		return err
	}
	root, err := l.open(folder)
	if err != nil {
		return err
	}
	defer root.Close()
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	return root.WriteFile("manga.json", append(b, '\n'), 0o644)
}

// Path is the series folder on disk (for mangadex-dl --path, or opening in Explorer).
func (l Library) Path(folder string) (string, error) {
	if !ValidName(folder) {
		return "", fmt.Errorf("bad folder name %q", folder)
	}
	return filepath.Join(l.Dir, folder), nil
}

// Cover is the series' cover.jpg.
func (l Library) Cover(folder string) ([]byte, error) {
	root, err := l.open(folder)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile("cover.jpg")
}

// Pages lists the images inside a .cbz, in reading order.
func (l Library) Pages(folder, file string) ([]string, error) {
	z, closeIt, err := l.zip(folder, file)
	if err != nil {
		return nil, err
	}
	defer closeIt()
	var pages []string
	for _, f := range z.File {
		if !f.FileInfo().IsDir() && imageType(f.Name) != "" {
			pages = append(pages, f.Name)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return Less(pages[i], pages[j]) })
	return pages, nil
}

// Page is page n (from 0) of a .cbz, with its content type.
func (l Library) Page(folder, file string, n int) ([]byte, string, error) {
	pages, err := l.Pages(folder, file)
	if err != nil {
		return nil, "", err
	}
	if n < 0 || n >= len(pages) {
		return nil, "", fmt.Errorf("no page %d (it has %d)", n+1, len(pages))
	}
	z, closeIt, err := l.zip(folder, file)
	if err != nil {
		return nil, "", err
	}
	defer closeIt()
	for _, f := range z.File {
		if f.Name != pages[n] {
			continue
		}
		if f.UncompressedSize64 > 64<<20 {
			return nil, "", fmt.Errorf("page %d is too big", n+1)
		}
		r, err := f.Open()
		if err != nil {
			return nil, "", err
		}
		defer r.Close()
		b, err := io.ReadAll(io.LimitReader(r, 64<<20))
		return b, imageType(f.Name), err
	}
	return nil, "", fmt.Errorf("page %d went missing", n+1)
}

// BookPath is a book's file on disk (to open a PDF/EPUB in her own reader).
func (l Library) BookPath(folder, file string) (string, error) {
	if !ValidName(folder) || !ValidName(file) {
		return "", fmt.Errorf("bad name")
	}
	p := filepath.Join(l.Dir, folder, file)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}

func (l Library) zip(folder, file string) (*zip.Reader, func(), error) {
	if !ValidName(file) || strings.ToLower(path.Ext(file)) != ".cbz" {
		return nil, nil, fmt.Errorf("only .cbz books open in the reader")
	}
	root, err := l.open(folder)
	if err != nil {
		return nil, nil, err
	}
	f, err := root.Open(file)
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		root.Close()
		return nil, nil, err
	}
	z, err := zip.NewReader(f, st.Size())
	if err != nil {
		f.Close()
		root.Close()
		return nil, nil, fmt.Errorf("%s isn't a readable .cbz: %w", file, err)
	}
	return z, func() { f.Close(); root.Close() }, nil
}

// open is the series folder as an os.Root: nothing reached through it can
// step outside the folder, whatever names the page sends.
func (l Library) open(folder string) (*os.Root, error) {
	if !ValidName(folder) {
		return nil, fmt.Errorf("bad folder name %q", folder)
	}
	return os.OpenRoot(filepath.Join(l.Dir, folder))
}

func readInfo(root *os.Root) (Info, error) {
	b, err := root.ReadFile("manga.json")
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		return Info{}, err
	}
	if info.Title == "" {
		return Info{}, fmt.Errorf("manga.json has no title")
	}
	return info, nil
}

// ValidName: one plain file or folder name, nothing that walks anywhere.
func ValidName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`) && !strings.ContainsRune(name, 0)
}

func imageType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".avif":
		return "image/avif"
	}
	return ""
}

// FolderName makes a title safe as a folder name on Windows (and everywhere else).
//
//	library.FolderName(`Re:Zero / Season 2?`) // "Re-Zero - Season 2"
func FolderName(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('-')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Join(strings.Fields(b.String()), " ")
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	name = strings.Trim(name, " .-")
	switch strings.ToUpper(name) {
	case "", "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		name += " manga"
	}
	return strings.TrimSpace(name)
}

// Less is "natural" order: Vol. 2 before Vol. 10, page 9 before page 10.
func Less(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da != "" && db != "" {
			na, nb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}
