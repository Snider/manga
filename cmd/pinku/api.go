// SPDX-License-Identifier: EUPL-1.2

package main

import (
	core "github.com/dappcore/go"

	"manga/downloader"
	"manga/library"
	"manga/mangadex"
	"manga/shelf"
)

// app is what the page talks to: the shelf, MangaDex, the download queue,
// and the window's own bits (folder picker, open-in-Explorer).
type app struct {
	shelf     *shelf.Shelf
	dex       *mangadex.Client
	down      *downloader.Service
	pick      func(title string) (string, error) // choose a folder (the window's dialog)
	pickBooks func() ([]string, error)           // choose book files to import
	imports   importer
	open      func(path string) error // open a folder/file with the OS
	browse    func(url string) error  // open a web page in her browser
	assets    core.Handler            // the page itself
	words     *words                  // the page's text, in her language
}

func (a *app) lib() library.Library { return library.Library{Dir: a.shelf.Settings().Library} }

// shelfSeries is a series plus where she got to, for the bookcase.
type shelfSeries struct {
	library.Series
	Progress map[string]shelf.Progress `json:"progress"`
	Read     int                       `json:"read"` // books finished
}

func (a *app) handler() core.Handler {
	mux := &core.ServeMux{}
	mux.HandleFunc("GET /api/shelf", func(w core.ResponseWriter, r *core.Request) {
		series, err := a.lib().Scan()
		if err != nil {
			fail(w, err.Error(), core.StatusInternalServerError)
			return
		}
		out := make([]shelfSeries, 0, len(series))
		for _, s := range series {
			p := a.shelf.Progress(s.Folder)
			read := 0
			for _, b := range s.Books {
				if p[b.File].Done() {
					read++
				}
			}
			out = append(out, shelfSeries{Series: s, Progress: p, Read: read})
		}
		send(w, map[string]any{"library": a.lib().Dir, "series": out, "busy": a.down.Busy()})
	})
	mux.HandleFunc("GET /api/cover/{folder}", func(w core.ResponseWriter, r *core.Request) {
		b, err := a.lib().Cover(r.PathValue("folder"))
		if err != nil {
			core.HTTPNotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
	mux.HandleFunc("GET /api/book/{folder}/{file}", func(w core.ResponseWriter, r *core.Request) {
		folder, file := r.PathValue("folder"), r.PathValue("file")
		pages, err := a.lib().Pages(folder, file)
		if err != nil {
			fail(w, err.Error(), core.StatusNotFound)
			return
		}
		send(w, map[string]any{"pages": len(pages), "page": a.shelf.Progress(folder)[file].Page})
	})
	mux.HandleFunc("GET /api/page/{folder}/{file}/{n}", func(w core.ResponseWriter, r *core.Request) {
		n, _ := core.Atoi(r.PathValue("n")).Value.(int)
		b, typ, err := a.lib().Page(r.PathValue("folder"), r.PathValue("file"), n)
		if err != nil {
			fail(w, err.Error(), core.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", typ)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(b)
	})
	mux.HandleFunc("POST /api/progress", func(w core.ResponseWriter, r *core.Request) {
		var p struct {
			Folder, File string
			Page, Pages  int
		}
		if !read(w, r, &p) {
			return
		}
		if res := a.shelf.SetPage(p.Folder, p.File, p.Page, p.Pages); !res.OK {
			fail(w, res.Error(), core.StatusBadRequest)
			return
		}
		w.WriteHeader(core.StatusNoContent)
	})
	mux.HandleFunc("POST /api/open", func(w core.ResponseWriter, r *core.Request) {
		var p struct{ Folder, File string }
		if !read(w, r, &p) {
			return
		}
		var path string
		var err error
		switch {
		case p.Folder == "":
			path = a.lib().Dir
			if r := core.MkdirAll(path, 0o755); !r.OK {
				err = r.Err()
			}
		case p.File == "":
			path, err = a.lib().Path(p.Folder)
		default:
			path, err = a.lib().BookPath(p.Folder, p.File)
		}
		if err == nil {
			err = a.open(path)
		}
		if err != nil {
			fail(w, err.Error(), core.StatusBadRequest)
			return
		}
		w.WriteHeader(core.StatusNoContent)
	})
	mux.HandleFunc("POST /api/browse", func(w core.ResponseWriter, r *core.Request) {
		var p struct{ URL string }
		if !read(w, r, &p) {
			return
		}
		parsed := core.URLParse(p.URL)
		u, _ := parsed.Value.(*core.URL)
		if !parsed.OK || u.Scheme != "https" || u.Host == "" {
			fail(w, "only web links open in the browser", core.StatusBadRequest)
			return
		}
		if err := a.browse(u.String()); err != nil {
			fail(w, err.Error(), core.StatusInternalServerError)
			return
		}
		w.WriteHeader(core.StatusNoContent)
	})
	mux.HandleFunc("GET /api/dexcover/{id}/{file}", func(w core.ResponseWriter, r *core.Request) {
		b, err := a.dex.Cover(r.Context(), r.PathValue("id"), r.PathValue("file"))
		if err != nil {
			core.HTTPNotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", core.HTTPDetectContentType(b))
		w.Header().Set("Cache-Control", "max-age=86400")
		w.Write(b)
	})
	mux.HandleFunc("GET /api/search", func(w core.ResponseWriter, r *core.Request) {
		q := r.URL.Query().Get("q")
		var found []mangadex.Manga
		var err error
		if id := mangadex.ID(q); id != "" { // she pasted a link
			var m mangadex.Manga
			if m, err = a.dex.Get(r.Context(), id); err == nil {
				found = []mangadex.Manga{m}
			}
		} else {
			found, err = a.dex.Search(r.Context(), q, a.shelf.Settings().Language)
		}
		if err != nil {
			fail(w, err.Error(), core.StatusBadGateway)
			return
		}
		if found == nil {
			found = []mangadex.Manga{}
		}
		owned := map[string]string{} // mangadex id → folder, so the page can say "on your shelf"
		if series, err := a.lib().Scan(); err == nil {
			for _, s := range series {
				if s.MangaDex != "" {
					owned[s.MangaDex] = s.Folder
				}
			}
		}
		send(w, map[string]any{"results": found, "owned": owned, "language": a.shelf.Settings().Language})
	})
	mux.HandleFunc("POST /api/add", func(w core.ResponseWriter, r *core.Request) {
		var m mangadex.Manga
		if !read(w, r, &m) {
			return
		}
		if mangadex.ID(m.ID) == "" || m.Title == "" {
			fail(w, "that doesn't look like a MangaDex manga", core.StatusBadRequest)
			return
		}
		m.ID = mangadex.ID(m.ID)
		job, err := a.down.Add(m)
		if err != nil {
			fail(w, err.Error(), core.StatusBadRequest)
			return
		}
		send(w, job)
	})
	mux.HandleFunc("POST /api/update", func(w core.ResponseWriter, r *core.Request) {
		var p struct{ Folder string } // "" = every series from MangaDex
		if !read(w, r, &p) {
			return
		}
		if p.Folder != "" {
			job, err := a.down.Update(p.Folder)
			if err != nil {
				fail(w, err.Error(), core.StatusBadRequest)
				return
			}
			send(w, []downloader.Job{job})
			return
		}
		series, _ := a.lib().Scan()
		jobs := []downloader.Job{}
		for _, s := range series {
			if s.Checkable {
				if job, err := a.down.Update(s.Folder); err == nil {
					jobs = append(jobs, job)
				}
			}
		}
		send(w, jobs)
	})
	mux.HandleFunc("GET /api/jobs", func(w core.ResponseWriter, r *core.Request) {
		send(w, a.down.Jobs())
	})
	mux.HandleFunc("GET /api/jobs/{id}/log", func(w core.ResponseWriter, r *core.Request) {
		id, _ := core.Atoi(r.PathValue("id")).Value.(int)
		send(w, a.down.Log(id))
	})
	mux.HandleFunc("POST /api/jobs/{id}/cancel", func(w core.ResponseWriter, r *core.Request) {
		id, _ := core.Atoi(r.PathValue("id")).Value.(int)
		a.down.Cancel(id)
		w.WriteHeader(core.StatusNoContent)
	})
	mux.HandleFunc("GET /api/version", func(w core.ResponseWriter, r *core.Request) {
		send(w, checkUpdate(r.Context(), a.shelf, core.Now()))
	})
	mux.HandleFunc("GET /api/strings", func(w core.ResponseWriter, r *core.Request) {
		if a.words == nil {
			send(w, map[string]any{"lang": "en", "auto": "en", "languages": []string{"en"}, "strings": map[string]string{}})
			return
		}
		lang, text := a.words.For(a.shelf.Settings().Words)
		send(w, map[string]any{"lang": lang, "auto": a.words.auto, "languages": a.words.langs, "strings": text})
	})
	mux.HandleFunc("GET /api/settings", func(w core.ResponseWriter, r *core.Request) {
		set := a.shelf.Settings()
		send(w, map[string]any{"settings": set, "formats": shelf.Formats, "tool": downloader.FindTool(set.Tool, shelf.Dir()), "canFetch": downloader.CanFetch()})
	})
	mux.HandleFunc("POST /api/settings", func(w core.ResponseWriter, r *core.Request) {
		var set shelf.Settings
		if !read(w, r, &set) {
			return
		}
		if res := a.shelf.Save(set); !res.OK {
			fail(w, res.Error(), core.StatusBadRequest)
			return
		}
		send(w, a.shelf.Settings())
	})
	mux.HandleFunc("POST /api/import", func(w core.ResponseWriter, r *core.Request) {
		if a.pickBooks == nil {
			fail(w, "no file picker here", core.StatusNotImplemented)
			return
		}
		paths, err := a.pickBooks()
		if err != nil {
			fail(w, err.Error(), core.StatusInternalServerError)
			return
		}
		send(w, map[string]bool{"started": a.Import(paths)}) // false: she cancelled, or one's running
	})
	mux.HandleFunc("GET /api/imports", func(w core.ResponseWriter, r *core.Request) {
		send(w, a.imports.state())
	})
	mux.HandleFunc("POST /api/pick-folder", func(w core.ResponseWriter, r *core.Request) {
		if a.pick == nil {
			fail(w, "no folder picker here", core.StatusNotImplemented)
			return
		}
		dir, err := a.pick("Where should the bookcase be?")
		if err != nil {
			fail(w, err.Error(), core.StatusInternalServerError)
			return
		}
		send(w, map[string]string{"folder": dir}) // "" = she cancelled
	})
	a.sourceRoutes(mux)
	mux.Handle("/", a.assets)
	return mux
}

// Import copies dropped or picked files onto the shelf, in the background.
func (a *app) Import(paths []string) bool { return a.imports.start(a.lib(), paths) }

func send(w core.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	core.JSONNewEncoder(w).Encode(v)
}

func fail(w core.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	core.JSONNewEncoder(w).Encode(map[string]string{"error": msg})
}

func read(w core.ResponseWriter, r *core.Request, v any) bool {
	if err := core.JSONNewDecoder(core.HTTPMaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		fail(w, "bad request: "+err.Error(), core.StatusBadRequest)
		return false
	}
	return true
}
