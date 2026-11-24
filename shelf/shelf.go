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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

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

var language = regexp.MustCompile(`^[a-z]{2,3}(-[a-z]{2,4})?$`)

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
	d, err := os.UserConfigDir()
	if err != nil {
		d = os.TempDir()
	}
	return movedFrom(filepath.Join(d, "MangaShelf"), filepath.Join(d, "Pinku"))
}

// movedFrom renames old to dir if only old exists, and answers the folder to
// use: old again if the rename couldn't happen (better than starting empty).
func movedFrom(old, dir string) string {
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	if _, err := os.Stat(old); err != nil {
		return dir
	}
	if os.Rename(old, dir) != nil {
		return old
	}
	return dir
}

// DefaultPath is where the store lives.
func DefaultPath() string { return filepath.Join(Dir(), "shelf.db") }

// DefaultLibrary is Documents\Manga: somewhere she'll find it in Explorer.
func DefaultLibrary() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(Dir(), "Manga")
	}
	return filepath.Join(home, "Documents", "Manga")
}

// Open opens (or creates) the store at path.
func Open(path string) core.Result {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return core.Fail(core.E("shelf.Open", "making "+filepath.Dir(path), err))
	}
	r := store.New(path, store.WithWorkspaceStateDirectory(filepath.Join(filepath.Dir(path), "state")))
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
	if v.Library == "" || !filepath.IsAbs(v.Library) {
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
		return core.Fail(core.E("shelf.SetPage", fmt.Sprintf("page %d of %d", page, pages), nil))
	}
	b, _ := json.Marshal(Progress{Page: page, Pages: pages, At: time.Now().Unix()})
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
		if json.Unmarshal([]byte(raw), &p) == nil {
			out[file] = p
		}
	}
	return out
}

// Checked remembers when a series last looked for new chapters.
func (s *Shelf) Checked(folder string, at time.Time) core.Result {
	return s.db.Set("checked", folder, strconv.FormatInt(at.Unix(), 10))
}

// LastChecked is when a series last looked for new chapters (zero if never).
func (s *Shelf) LastChecked(folder string) time.Time {
	v, r := s.db.Get("checked", folder)
	if !r.OK {
		return time.Time{}
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return time.Unix(n, 0)
}
