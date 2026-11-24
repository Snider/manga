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
// Every read and write goes through a go-io local medium rooted at the
// bookcase, so nothing the page sends can reach outside it.
//
//	lib := library.Library{Dir: `C:\Users\her\Documents\Manga`}
//	series, err := lib.Scan()
//	pages, err := lib.Pages(series[0].Folder, series[0].Books[0].File)
package library

import (
	"archive/zip"

	core "github.com/dappcore/go"
	"github.com/dappcore/go-io/go/local"
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
	Added    core.Time `json:"added"`
}

var formats = map[string]bool{".cbz": true, ".pdf": true, ".epub": true, ".cb7": true}

// pageLimit caps one page read from a .cbz (a hostile zip can claim anything).
const pageLimit = 64 << 20

// Scan lists every series, newest-added first. A missing folder is an empty shelf.
func (l Library) Scan() ([]Series, error) {
	if !core.Stat(l.Dir).OK {
		return []Series{}, nil
	}
	m, err := l.medium()
	if err != nil {
		return nil, err
	}
	defer m.Close()
	entries, err := m.List("")
	if err != nil {
		return nil, err
	}
	out := []Series{}
	for _, e := range entries {
		if !e.IsDir() || core.HasPrefix(e.Name(), ".") {
			continue
		}
		s, err := series(m, e.Name())
		if err != nil || (len(s.Books) == 0 && s.MangaDex == "") {
			continue // not a series (or one still being started)
		}
		out = append(out, s)
	}
	core.SliceSortFunc(out, func(a, b Series) bool {
		if a.Added != b.Added {
			return a.Added > b.Added
		}
		return Less(a.Folder, b.Folder)
	})
	return out, nil
}

// Series reads one series folder.
func (l Library) Series(folder string) (Series, error) {
	m, err := l.open(folder)
	if err != nil {
		return Series{}, err
	}
	defer m.Close()
	return series(m, folder)
}

func series(m *local.Medium, folder string) (Series, error) {
	s := Series{Folder: folder, Title: folder, Books: []Book{}}
	if st, err := m.Stat(folder); err == nil {
		s.Added = st.ModTime().Unix()
	}
	if info, err := readInfo(m, folder); err == nil {
		s.MangaDex, s.Title, s.Added = info.MangaDex, info.Title, info.Added.Unix()
	}
	s.HasCover = m.IsFile(folder + "/cover.jpg")
	entries, err := m.List(folder)
	if err != nil {
		return s, err
	}
	for _, e := range entries {
		ext := core.Lower(core.PathExt(e.Name()))
		if e.IsDir() || !formats[ext] || core.HasPrefix(e.Name(), ".") {
			continue
		}
		size := int64(0)
		if fi, err := e.Info(); err == nil {
			size = fi.Size()
		}
		s.Books = append(s.Books, Book{File: e.Name(), Name: core.TrimSuffix(e.Name(), core.PathExt(e.Name())), Format: ext[1:], Size: size})
	}
	core.SliceSortFunc(s.Books, func(a, b Book) bool { return Less(a.Name, b.Name) })
	return s, nil
}

// Info reads a series' manga.json (an error if it has none).
func (l Library) Info(folder string) (Info, error) {
	m, err := l.open(folder)
	if err != nil {
		return Info{}, err
	}
	defer m.Close()
	return readInfo(m, folder)
}

// WriteInfo creates the series folder (if need be) and its manga.json.
func (l Library) WriteInfo(folder string, info Info) error {
	if !ValidName(folder) {
		return core.E("library.WriteInfo", core.Sprintf("bad folder name %q", folder), nil)
	}
	if r := core.MkdirAll(l.Dir, 0o755); !r.OK {
		return core.E("library.WriteInfo", "make the bookcase folder", r.Err())
	}
	m, err := l.medium()
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.EnsureDir(folder); err != nil {
		return err
	}
	b := core.JSONMarshalIndent(info, "", "  ")
	if !b.OK {
		return core.E("library.WriteInfo", "encode manga.json", b.Err())
	}
	return m.Write(folder+"/manga.json", string(b.Bytes())+"\n")
}

// Path is the series folder on disk (for mangadex-dl --path, or opening in Explorer).
func (l Library) Path(folder string) (string, error) {
	if !ValidName(folder) {
		return "", core.E("library.Path", core.Sprintf("bad folder name %q", folder), nil)
	}
	return core.PathJoin(l.Dir, folder), nil
}

// Cover is the series' cover.jpg.
func (l Library) Cover(folder string) ([]byte, error) {
	m, err := l.open(folder)
	if err != nil {
		return nil, err
	}
	defer m.Close()
	s, err := m.Read(folder + "/cover.jpg")
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// Pages lists the images inside a .cbz, in reading order.
func (l Library) Pages(folder, file string) ([]string, error) {
	z, closeIt, err := l.zip(folder, file)
	if err != nil {
		return nil, err
	}
	defer closeIt()
	return pages(z), nil
}

func pages(z *zip.Reader) []string {
	var out []string
	for _, f := range z.File {
		if !f.FileInfo().IsDir() && imageType(f.Name) != "" {
			out = append(out, f.Name)
		}
	}
	core.SliceSortFunc(out, Less)
	return out
}

// Page is page n (from 0) of a .cbz, with its content type.
func (l Library) Page(folder, file string, n int) ([]byte, string, error) {
	z, closeIt, err := l.zip(folder, file)
	if err != nil {
		return nil, "", err
	}
	defer closeIt()
	all := pages(z)
	if n < 0 || n >= len(all) {
		return nil, "", core.E("library.Page", core.Sprintf("no page %d (it has %d)", n+1, len(all)), nil)
	}
	for _, f := range z.File {
		if f.Name != all[n] {
			continue
		}
		if f.UncompressedSize64 > pageLimit {
			return nil, "", core.E("library.Page", core.Sprintf("page %d is too big", n+1), nil)
		}
		r, err := f.Open()
		if err != nil {
			return nil, "", err
		}
		got := core.ReadAll(core.LimitReader(r, pageLimit)) // closes r
		if !got.OK {
			return nil, "", core.E("library.Page", core.Sprintf("read page %d", n+1), got.Err())
		}
		return []byte(got.Value.(string)), imageType(f.Name), nil
	}
	return nil, "", core.E("library.Page", core.Sprintf("page %d went missing", n+1), nil)
}

// BookPath is a book's file on disk (to open a PDF/EPUB in her own reader).
func (l Library) BookPath(folder, file string) (string, error) {
	if !ValidName(folder) || !ValidName(file) {
		return "", core.E("library.BookPath", "bad name", nil)
	}
	p := core.PathJoin(l.Dir, folder, file)
	if r := core.Stat(p); !r.OK {
		return "", r.Err()
	}
	return p, nil
}

// readerAt is what zip.NewReader reads through: the medium's open file.
type readerAt interface {
	ReadAt(p []byte, off int64) (int, error)
}

func (l Library) zip(folder, file string) (*zip.Reader, func(), error) {
	if !ValidName(file) || core.Lower(core.PathExt(file)) != ".cbz" {
		return nil, nil, core.E("library.zip", "only .cbz books open in the reader", nil)
	}
	m, err := l.open(folder)
	if err != nil {
		return nil, nil, err
	}
	f, err := m.Open(folder + "/" + file)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	done := func() { f.Close(); m.Close() }
	st, err := f.Stat()
	if err != nil {
		done()
		return nil, nil, err
	}
	ra, ok := f.(readerAt)
	if !ok {
		done()
		return nil, nil, core.E("library.zip", "the book can't be read in place", nil)
	}
	z, err := zip.NewReader(ra, st.Size())
	if err != nil {
		done()
		return nil, nil, core.E("library.zip", file+" isn't a readable .cbz", err)
	}
	return z, done, nil
}

// medium is the bookcase as a go-io local medium: nothing reached through it
// can step outside the folder, whatever names the page sends.
func (l Library) medium() (*local.Medium, error) {
	m, err := local.New(l.Dir)
	if err != nil {
		return nil, core.E("library", "open the bookcase", err)
	}
	return m, nil
}

// open is medium, once folder is known to be one plain name.
func (l Library) open(folder string) (*local.Medium, error) {
	if !ValidName(folder) {
		return nil, core.E("library", core.Sprintf("bad folder name %q", folder), nil)
	}
	m, err := l.medium()
	if err != nil {
		return nil, err
	}
	if !m.IsDir(folder) {
		m.Close()
		return nil, core.E("library", core.Sprintf("no series %q", folder), core.ErrNotExist)
	}
	return m, nil
}

func readInfo(m *local.Medium, folder string) (Info, error) {
	s, err := m.Read(folder + "/manga.json")
	if err != nil {
		return Info{}, err
	}
	var info Info
	if r := core.JSONUnmarshalString(s, &info); !r.OK {
		return Info{}, core.E("library.readInfo", "manga.json", r.Err())
	}
	if info.Title == "" {
		return Info{}, core.E("library.readInfo", "manga.json has no title", nil)
	}
	return info, nil
}

// ValidName: one plain file or folder name, nothing that walks anywhere.
func ValidName(name string) bool {
	return name != "" && name != "." && name != ".." && !core.ContainsAny(name, `/\`) && !core.ContainsRune(name, 0)
}

func imageType(name string) string {
	switch core.Lower(core.PathExt(name)) {
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
	b := core.NewBuilder()
	for _, r := range title {
		switch {
		case core.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('-')
		case core.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	name := core.Join(" ", core.Fields(b.String())...)
	for core.Contains(name, "--") {
		name = core.Replace(name, "--", "-")
	}
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	name = core.TrimCutset(name, " .-")
	switch core.Upper(name) {
	case "", "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		name += " manga"
	}
	return core.Trim(name)
}

// Less is "natural" order: Vol. 2 before Vol. 10, page 9 before page 10.
func Less(a, b string) bool {
	a, b = core.Lower(a), core.Lower(b)
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da != "" && db != "" {
			na, nb := core.TrimLeft(da, "0"), core.TrimLeft(db, "0")
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
