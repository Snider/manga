// SPDX-License-Identifier: EUPL-1.2

package main

import (
	core "github.com/dappcore/go"

	"manga/opds"
	"manga/shelf"
)

// sourceView is one of her catalogues as the page sees it: never the password.
type sourceView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	User        string `json:"user,omitempty"`
	HasPassword bool   `json:"hasPassword"`
}

// catalogueResults is what one catalogue found for a search.
type catalogueResults struct {
	Source  sourceView   `json:"source"`
	Results []opds.Entry `json:"results"`
	Error   string       `json:"error,omitempty"`
}

// catalogueOf turns a saved source into something to read from.
func catalogueOf(src shelf.Source) opds.Catalogue {
	return opds.Catalogue{URL: src.URL, User: src.User, Password: src.Password, Agent: "Pinku/" + version + " (+https://github.com/Snider/manga)"}
}

func viewOf(src shelf.Source) sourceView {
	return sourceView{ID: src.ID, Name: src.Name, URL: src.URL, User: src.User, HasPassword: src.Password != ""}
}

// sourceRoutes: her catalogues, searching them, their covers, and adding
// from them. Part of handler().
func (a *app) sourceRoutes(mux *core.ServeMux) {
	mux.HandleFunc("GET /api/sources", func(w core.ResponseWriter, r *core.Request) {
		out := []sourceView{}
		for _, src := range a.shelf.Sources() {
			out = append(out, viewOf(src))
		}
		send(w, out)
	})
	mux.HandleFunc("POST /api/sources", func(w core.ResponseWriter, r *core.Request) {
		var src shelf.Source
		if !read(w, r, &src) {
			return
		}
		res := a.shelf.SaveSource(src)
		if !res.OK {
			fail(w, res.Error(), core.StatusBadRequest)
			return
		}
		saved := res.Value.(shelf.Source)
		// Try it, so Settings can say straight away if the link or login is wrong.
		_, err := catalogueOf(saved).Search(r.Context(), "a")
		check := ""
		if err != nil {
			check = err.Error()
		}
		send(w, map[string]any{"source": viewOf(saved), "check": check})
	})
	mux.HandleFunc("POST /api/sources/{id}/remove", func(w core.ResponseWriter, r *core.Request) {
		a.shelf.RemoveSource(r.PathValue("id"))
		w.WriteHeader(core.StatusNoContent)
	})
	mux.HandleFunc("GET /api/sources/search", func(w core.ResponseWriter, r *core.Request) {
		send(w, a.searchCatalogues(r.Context(), r.URL.Query().Get("q")))
	})
	mux.HandleFunc("GET /api/sources/{id}/cover", func(w core.ResponseWriter, r *core.Request) {
		src, ok := a.shelf.SourceByID(r.PathValue("id"))
		target := r.URL.Query().Get("url")
		if !ok || !sameHost(src.URL, target) {
			core.HTTPNotFound(w, r) // only covers from that catalogue's own server
			return
		}
		buf := core.NewBuffer()
		cat := catalogueOf(src)
		cat.StayHome = true // a redirect can't take the proxy elsewhere
		if err := cat.Fetch(r.Context(), target, &limited{w: buf, left: 4 << 20}); err != nil {
			core.HTTPNotFound(w, r)
			return
		}
		serveImage(w, r, buf.Bytes(), "max-age=86400")
	})
	mux.HandleFunc("POST /api/sources/{id}/add", func(w core.ResponseWriter, r *core.Request) {
		var e opds.Entry
		if !read(w, r, &e) {
			return
		}
		job, err := a.down.AddFromCatalogue(r.PathValue("id"), e)
		if err != nil {
			fail(w, err.Error(), core.StatusBadRequest)
			return
		}
		send(w, job)
	})
}

// searchCatalogues asks every catalogue at once; a slow or broken one says
// so in its own results and doesn't hold up the others.
func (a *app) searchCatalogues(ctx core.Context, q string) []catalogueResults {
	sources := a.shelf.Sources()
	out := make([]catalogueResults, len(sources))
	var wg core.WaitGroup
	for i, src := range sources {
		out[i].Source = viewOf(src)
		wg.Go(func() {
			ctx, cancel := core.WithTimeout(ctx, 20*core.Second)
			defer cancel()
			found, err := catalogueOf(src).Search(ctx, q)
			if err != nil {
				out[i].Error = err.Error()
			}
			if found == nil {
				found = []opds.Entry{}
			}
			for j := range found { // covers through the app, with her login
				if found[j].Cover != "" {
					found[j].Cover = "/api/sources/" + core.URLPathEscape(src.ID) + "/cover?url=" + core.URLEncode(found[j].Cover)
				}
			}
			out[i].Results = found
		})
	}
	wg.Wait()
	return out
}

// sameHost: is target a web link on the same server as root?
func sameHost(root, target string) bool {
	a, b := core.URLParse(root), core.URLParse(target)
	if !a.OK || !b.OK {
		return false
	}
	ra, rb := a.Value.(*core.URL), b.Value.(*core.URL)
	return (rb.Scheme == "http" || rb.Scheme == "https") && ra.Scheme == rb.Scheme && core.EqualFold(ra.Host, rb.Host)
}

// limited is a writer that refuses past its size (a cover, not a film).
type limited struct {
	w    core.Writer
	left int64
}

func (l *limited) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, core.E("pinku.cover", "too big for a cover", nil)
	}
	l.left -= int64(len(p))
	return l.w.Write(p)
}
