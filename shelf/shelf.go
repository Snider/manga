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
func Dir() string {
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
	for k, val := range map[string]string{"library": v.Library, "language": v.Language, "format": v.Format, "tool": v.Tool} {
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
