// SPDX-License-Identifier: EUPL-1.2

package library

import (
	"archive/zip"
	"encoding/xml"

	core "github.com/dappcore/go"
	"github.com/dappcore/go-io/go/local"
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
// one with the same name is copied as "name (2).cbz". The dropped files are
// read from wherever they are; everything written goes through the
// bookcase's medium, so it lands inside the bookcase or not at all.
func (l Library) Import(paths []string) Imported {
	var out Imported
	if r := core.MkdirAll(l.Dir, 0o755); !r.OK {
		out.Failed = append(out.Failed, "the bookcase folder: "+r.Error())
		return out
	}
	m, err := l.medium()
	if err != nil {
		out.Failed = append(out.Failed, "the bookcase folder: "+err.Error())
		return out
	}
	defer m.Close()
	touched := map[string]bool{}
	add := func(series, src string) {
		folder := FolderName(series)
		dst, state, err := l.copyBook(m, folder, src)
		switch {
		case err != nil:
			out.Failed = append(out.Failed, core.PathBase(src)+": "+err.Error())
		case state == "same":
			out.Same = append(out.Same, folder+"/"+core.PathBase(src))
		default:
			out.Added = append(out.Added, folder+"/"+dst)
			touched[folder] = true
		}
	}
	for _, p := range paths {
		st := core.Stat(p)
		if !st.OK {
			out.Failed = append(out.Failed, core.PathBase(p)+": can't read it")
			continue
		}
		if !st.Value.(core.FsFileInfo).IsDir() {
			if !IsBook(p) {
				out.Skipped = append(out.Skipped, core.PathBase(p))
				continue
			}
			add(SeriesOf(p), p)
			continue
		}
		books, subdirs := listDir(p)
		for _, b := range books { // a series folder
			add(core.PathBase(p), b)
		}
		for _, d := range subdirs { // a folder of series folders (one level down)
			inner, _ := listDir(d)
			for _, b := range inner {
				add(core.PathBase(d), b)
			}
		}
		if len(books) == 0 && len(subdirs) == 0 {
			out.Skipped = append(out.Skipped, core.PathBase(p)+" (no books in it)")
		}
	}
	for folder := range touched {
		out.Series = append(out.Series, folder)
		l.ensureCover(m, folder)
	}
	core.SliceSort(out.Series)
	return out
}

// IsBook: a file the shelf shows (.cbz, .pdf, .epub, .cb7).
func IsBook(path string) bool { return formats[core.Lower(core.PathExt(path))] }

var volumeTail = core.MustRegex(`(?i)[\s._-]*[\(\[]?(?:\b(?:v|vol|volume|c|ch|chap|chapter|t|tome|book)\.?|#)\s*\d+.*$`)

// SeriesOf names the series a dropped book belongs to: ComicInfo.xml's
// <Series> if it's a .cbz that has one, else the file name without its
// volume or chapter ("Berserk v01" → "Berserk", "One Piece - Vol. 3" → "One Piece").
func SeriesOf(path string) string {
	if core.EqualFold(core.PathExt(path), ".cbz") {
		if s := comicInfoSeries(path); s != "" {
			return s
		}
	}
	name := core.TrimSuffix(core.PathBase(path), core.PathExt(path))
	name = core.Replace(name, "_", " ")
	if s := core.Trim(volumeTail.ReplaceAllString(name, "")); s != "" {
		name = s
	}
	return core.TrimRight(core.Trim(name), " -.")
}

func comicInfoSeries(path string) string {
	z, err := zip.OpenReader(path)
	if err != nil {
		return ""
	}
	defer z.Close()
	for _, f := range z.File {
		if !core.EqualFold(core.PathBase(f.Name), "ComicInfo.xml") || f.UncompressedSize64 > 1<<20 {
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
		if xml.NewDecoder(core.LimitReader(r, 1<<20)).Decode(&info) != nil {
			return ""
		}
		return core.Trim(info.Series)
	}
	return ""
}

// listDir: the books in a folder, and its sub-folders (not hidden ones).
func listDir(dir string) (books, subdirs []string) {
	r := core.ReadDir(core.DirFS(dir), ".")
	if !r.OK {
		return nil, nil
	}
	for _, e := range r.Value.([]core.FsDirEntry) {
		if core.HasPrefix(e.Name(), ".") {
			continue
		}
		p := core.PathJoin(dir, e.Name())
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
func (l Library) copyBook(m *local.Medium, folder, src string) (string, string, error) {
	if !ValidName(folder) {
		return "", "", core.E("library.Import", core.Sprintf("can't make a folder called %q", folder), nil)
	}
	from, to := core.PathAbs(core.PathDir(src)), core.PathAbs(core.PathJoin(l.Dir, folder))
	if from.OK && to.OK && from.Value.(string) == to.Value.(string) {
		return "", "same", nil // dropped from the shelf onto the shelf
	}
	if err := m.EnsureDir(folder); err != nil {
		return "", "", err
	}
	st := core.Stat(src)
	if !st.OK {
		return "", "", st.Err()
	}
	size := st.Value.(core.FsFileInfo).Size()
	base := core.PathBase(src)
	ext := core.PathExt(base)
	name := base
	for n := 2; ; n++ {
		existing, err := m.Stat(folder + "/" + name)
		if err != nil {
			break // free
		}
		if existing.Size() == size {
			return name, "same", nil
		}
		name = core.TrimSuffix(base, ext) + " (" + core.Itoa(n) + ")" + ext
	}
	in := core.Open(src)
	if !in.OK {
		return "", "", in.Err()
	}
	defer core.CloseStream(in.Value)
	tmp := folder + "/.importing-" + name + ".part" // hidden, and not a book while it copies
	w, err := m.Create(tmp)
	if err != nil {
		return "", "", err
	}
	copied := core.Copy(w, in.Value.(core.Reader))
	if cerr := w.Close(); copied.OK && cerr != nil {
		copied = core.Result{Value: cerr, OK: false}
	}
	if !copied.OK {
		m.Delete(tmp)
		return "", "", copied.Err()
	}
	if err := m.Rename(tmp, folder+"/"+name); err != nil {
		m.Delete(tmp)
		return "", "", err
	}
	return name, "added", nil
}

// Save writes a book into a series folder through write (a download, say):
// to a hidden .part file first, renamed into place only when write succeeds,
// so a book never shows on the shelf half-written. A name that's taken gets
// "(2)". It answers the file's name.
//
//	name, err := lib.Save("Frieren", "Frieren v01.cbz", func(w core.Writer) error { return cat.Fetch(ctx, url, w) })
func (l Library) Save(folder, name string, write func(core.Writer) error) (string, error) {
	if !ValidName(folder) || !ValidName(name) || core.HasPrefix(name, ".") {
		return "", core.E("library.Save", core.Sprintf("bad name %q/%q", folder, name), nil)
	}
	if r := core.MkdirAll(l.Dir, 0o755); !r.OK {
		return "", r.Err()
	}
	m, err := l.medium()
	if err != nil {
		return "", err
	}
	defer m.Close()
	if err := m.EnsureDir(folder); err != nil {
		return "", err
	}
	ext := core.PathExt(name)
	final := name
	for n := 2; m.Exists(folder + "/" + final); n++ {
		final = core.TrimSuffix(name, ext) + " (" + core.Itoa(n) + ")" + ext
	}
	tmp := folder + "/.saving-" + final + ".part"
	w, err := m.Create(tmp)
	if err != nil {
		return "", err
	}
	werr := write(w)
	if cerr := w.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		m.Delete(tmp)
		return "", werr
	}
	if err := m.Rename(tmp, folder+"/"+final); err != nil {
		m.Delete(tmp)
		return "", err
	}
	return final, nil
}

// EnsureCover gives a series without a cover.jpg the first page of its first .cbz.
func (l Library) EnsureCover(folder string) {
	m, err := l.open(folder)
	if err != nil {
		return
	}
	defer m.Close()
	l.ensureCover(m, folder)
}

// ensureCover gives a series without a cover.jpg the first page of its first .cbz.
func (l Library) ensureCover(m *local.Medium, folder string) {
	s, err := series(m, folder)
	if err != nil || s.HasCover {
		return
	}
	for _, b := range s.Books {
		if !b.Readable() {
			continue
		}
		page, _, err := l.Page(folder, b.File, 0)
		if err == nil && len(page) > 0 {
			m.Write(folder+"/cover.jpg", string(page)) // browsers sniff png/webp too
		}
		return
	}
}
