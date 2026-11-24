// SPDX-License-Identifier: EUPL-1.2

// Package shelf is what Pinku remembers between runs, in a go-store
// (SQLite) file in its own folder: the settings, and where she got to in
// each book.
//
//	%AppData%\Pinku\shelf.db          (Windows)
//	~/Library/Application Support/Pinku/shelf.db  (macOS)
//
//	s := shelf.Open(shelf.DefaultPath()).Value.(*shelf.Shelf)
//	s.Settings().Library             // C:\Users\her\Documents\Manga
//	s.SetPage("Uso-douri…", "Vol. 1.cbz", 12, 40)
package shelf

import (
	core "github.com/dappcore/go"
	store "github.com/dappcore/go-store/go"
)

// Settings is everything she can change.
type Settings struct {
	Library  string `json:"library"`  // the bookcase folder
	Language string `json:"language"` // what to download (MangaDex code: en, fr, es-la, pt-br…)
	Format   string `json:"format"`   // mangadex-dl --save-as: cbz-volume (a file per volume) or cbz (per chapter)
	Tool     string `json:"tool"`     // mangadex-dl, if it isn't found by itself ("" = find it)
	Words    string `json:"words"`    // the language Pinku itself speaks ("" = same as Windows)
}

// Formats she can choose between (the reader opens all of them).
var Formats = map[string]string{
	"cbz-volume": "One file per volume",
	"cbz":        "One file per chapter",
}

var language = core.MustRegex(`^[a-z]{2,3}(-[a-z]{2,4})?$`)

// ValidLanguage: a MangaDex language code (en, pt-br, es-la, ja-ro).
func ValidLanguage(code string) bool { return language.MatchString(code) }

// Progress is where she got to in one book.
type Progress struct {
	Page  int   `json:"page"`  // from 0
	Pages int   `json:"pages"` // how many it has
	At    int64 `json:"at"`    // unix seconds, last read
}

// Done: on (or past) the last page.
func (p Progress) Done() bool { return p.Pages > 0 && p.Page >= p.Pages-1 }

// Shelf is the open store.
type Shelf struct {
	db *store.Store
}

// Dir is Pinku's own folder (the store, and mangadex-dl if it fetched it).
// It was called MangaShelf before the app had its name; that folder is moved
// over the first time, so her settings and where she was reading come along.
//
// PINKU_HOME (a dev tag, never set for her) moves everything, store and
// default bookcase, into one folder: a test copy can't touch a real shelf.
func Dir() string {
	if dev := core.Getenv("PINKU_HOME"); dev != "" {
		return dev
	}
	d := core.TempDir()
	if r := core.UserConfigDir(); r.OK {
		d = r.Value.(string)
	}
	return movedFrom(core.PathJoin(d, "MangaShelf"), core.PathJoin(d, "Pinku"))
}

// movedFrom renames old to dir if only old exists, and answers the folder to
// use: old again if the rename couldn't happen (better than starting empty).
func movedFrom(old, dir string) string {
	if core.Stat(dir).OK || !core.Stat(old).OK {
		return dir
	}
	if !core.Rename(old, dir).OK {
		return old
	}
	return dir
}

// DefaultPath is where the store lives.
func DefaultPath() string { return core.PathJoin(Dir(), "shelf.db") }

// DefaultLibrary is Documents\Manga: somewhere she'll find it in Explorer.
func DefaultLibrary() string {
	if dev := core.Getenv("PINKU_HOME"); dev != "" {
		return core.PathJoin(dev, "Manga")
	}
	home := core.UserHomeDir()
	if !home.OK {
		return core.PathJoin(Dir(), "Manga")
	}
	return core.PathJoin(home.Value.(string), "Documents", "Manga")
}

// Open opens (or creates) the store at path.
func Open(path string) core.Result {
	if r := core.MkdirAll(core.PathDir(path), 0o755); !r.OK {
		return core.Fail(core.E("shelf.Open", "making "+core.PathDir(path), r.Err()))
	}
	r := store.New(path, store.WithWorkspaceStateDirectory(core.PathJoin(core.PathDir(path), "state")))
	if !r.OK {
		return r
	}
	return core.Ok(&Shelf{db: r.Value.(*store.Store)})
}

// Close closes the store.
func (s *Shelf) Close() core.Result { return s.db.Close() }

// Settings is the settings, defaults filled in.
func (s *Shelf) Settings() Settings {
	v := Settings{Library: DefaultLibrary(), Language: "en", Format: "cbz-volume"}
	all, r := s.db.GetAll("settings")
	if !r.OK {
		return v
	}
	if all["library"] != "" {
		v.Library = all["library"]
	}
	if all["language"] != "" {
		v.Language = all["language"]
	}
	if Formats[all["format"]] != "" {
		v.Format = all["format"]
	}
	v.Tool = all["tool"]
	if all["words"] == "" || ValidLanguage(all["words"]) {
		v.Words = all["words"]
	}
	return v
}

// Save checks and stores the settings.
func (s *Shelf) Save(v Settings) core.Result {
	if v.Library == "" || !core.PathIsAbs(v.Library) {
		return core.Fail(core.E("shelf.Save", "the bookcase needs a full folder path", nil))
	}
	if Formats[v.Format] == "" {
		return core.Fail(core.E("shelf.Save", "unknown format "+v.Format, nil))
	}
	if !ValidLanguage(v.Language) {
		return core.Fail(core.E("shelf.Save", "language is a code like en or pt-br", nil))
	}
	if v.Words != "" && !ValidLanguage(v.Words) {
		return core.Fail(core.E("shelf.Save", "Pinku's language is a code like en or tr, or empty for Windows'", nil))
	}
	for k, val := range map[string]string{"library": v.Library, "language": v.Language, "format": v.Format, "tool": v.Tool, "words": v.Words} {
		if r := s.db.Set("settings", k, val); !r.OK {
			return r
		}
	}
	return core.Ok(v)
}

// SetPage remembers where she is in a book.
func (s *Shelf) SetPage(folder, file string, page, pages int) core.Result {
	if page < 0 || pages <= 0 || page >= pages {
		return core.Fail(core.E("shelf.SetPage", core.Sprintf("page %d of %d", page, pages), nil))
	}
	b := core.JSONMarshal(Progress{Page: page, Pages: pages, At: core.UnixNow()}).Bytes()
	return s.db.Set("progress:"+folder, file, string(b))
}

// Progress is every book she's opened in a series (file → progress).
func (s *Shelf) Progress(folder string) map[string]Progress {
	out := map[string]Progress{}
	all, r := s.db.GetAll("progress:" + folder)
	if !r.OK {
		return out
	}
	for file, raw := range all {
		var p Progress
		if core.JSONUnmarshalString(raw, &p).OK {
			out[file] = p
		}
	}
	return out
}

// Checked remembers when a series last looked for new chapters.
func (s *Shelf) Checked(folder string, at core.Time) core.Result {
	return s.db.Set("checked", folder, core.FormatInt(at.Unix(), 10))
}

// LastChecked is when a series last looked for new chapters (zero if never).
func (s *Shelf) LastChecked(folder string) core.Time {
	v, r := s.db.Get("checked", folder)
	if !r.OK {
		return core.Time{}
	}
	n, _ := core.ParseInt(v, 10, 64).Value.(int64)
	return core.Unix(n, 0)
}

// Note remembers a small fact about Pinku itself (the update check's result).
func (s *Shelf) Note(key, value string) core.Result {
	return s.db.Set("app", key, value)
}

// Noted is a fact Note remembered ("" if none).
func (s *Shelf) Noted(key string) string {
	v, r := s.db.Get("app", key)
	if !r.OK {
		return ""
	}
	return v
}

// Source is an OPDS catalogue she added (Komga, Kavita, Calibre-web, a shop).
// Password is kept here for Pinku's own use only: the page is never sent it.
type Source struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}

// Sources lists her catalogues, in the order she added them.
func (s *Shelf) Sources() []Source {
	all, r := s.db.GetAll("sources")
	if !r.OK {
		return nil
	}
	out := make([]Source, 0, len(all))
	for _, raw := range all {
		var src Source
		if core.JSONUnmarshalString(raw, &src).OK && src.ID != "" {
			out = append(out, src)
		}
	}
	core.SliceSortFunc(out, func(a, b Source) bool { return a.ID < b.ID })
	return out
}

// SourceByID is one of her catalogues.
func (s *Shelf) SourceByID(id string) (Source, bool) {
	for _, src := range s.Sources() {
		if src.ID == id {
			return src, true
		}
	}
	return Source{}, false
}

// SaveSource adds a catalogue (ID "") or changes one. A web link is needed;
// an empty Password on a change keeps the one already saved.
func (s *Shelf) SaveSource(src Source) core.Result {
	src.Name, src.URL, src.User = core.Trim(src.Name), core.Trim(src.URL), core.Trim(src.User)
	u := core.URLParse(src.URL)
	if !u.OK || (u.Value.(*core.URL).Scheme != "http" && u.Value.(*core.URL).Scheme != "https") || u.Value.(*core.URL).Host == "" {
		return core.Fail(core.E("shelf.SaveSource", "a catalogue needs a web link (https://…)", nil))
	}
	if src.Name == "" {
		src.Name = u.Value.(*core.URL).Host
	}
	if src.ID == "" {
		src.ID = core.Sprintf("s%d", core.Now().UnixNano())
	} else if old, ok := s.SourceByID(src.ID); ok && src.Password == "" && src.User == old.User {
		src.Password = old.Password
	} else if !ok {
		return core.Fail(core.E("shelf.SaveSource", "no catalogue "+src.ID, nil))
	}
	b := core.JSONMarshal(src)
	if !b.OK {
		return b
	}
	if r := s.db.Set("sources", src.ID, string(b.Bytes())); !r.OK {
		return r
	}
	return core.Ok(src)
}

// RemoveSource forgets a catalogue (what's on the shelf from it stays).
func (s *Shelf) RemoveSource(id string) core.Result {
	return s.db.Delete("sources", id)
}
