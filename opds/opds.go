// SPDX-License-Identifier: EUPL-1.2

// Package opds reads OPDS 1.2 catalogues: the Atom feeds that Komga, Kavita,
// Calibre-web and many publishers' stores serve. Pinku uses one as a source:
// search it, list a series' books, and fetch a book's file onto the shelf.
//
//	c := opds.Catalogue{URL: "https://komga.home/opds/v1.2/catalog", User: "her", Password: "…"}
//	found, err := c.Search(ctx, "frieren")      // series and books matching
//	books, err := c.Books(ctx, found[0].Feed)   // a series' books, every page
//	err = c.Fetch(ctx, books[0].File, w)        // the .cbz, into w
package opds

import (
	"encoding/xml"

	core "github.com/dappcore/go"
)

// Catalogue is one OPDS catalogue she added.
type Catalogue struct {
	URL      string // the root feed
	User     string // for a server that asks (Komga, Kavita): sent only to the catalogue's own host
	Password string
	HTTP     *core.HTTPClient // nil: one with a timeout
	Agent    string
}

// Entry is a search result: a series (Feed is where its books are) or a
// single book (Books has it).
type Entry struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Author  string `json:"author,omitempty"`
	Summary string `json:"summary,omitempty"`
	Cover   string `json:"cover,omitempty"` // absolute URL of the thumbnail
	Feed    string `json:"feed,omitempty"`  // absolute URL of the series' feed
	Books   []Book `json:"books,omitempty"`
}

// Book is one file a catalogue offers.
type Book struct {
	Title  string `json:"title"`
	File   string `json:"file"`   // absolute URL
	Format string `json:"format"` // cbz, epub, pdf
	Size   int64  `json:"size,omitempty"`
}

// formats Pinku can shelve, by the types catalogues send.
var formats = map[string]string{
	"application/zip":               "cbz",
	"application/x-cbz":             "cbz",
	"application/vnd.comicbook+zip": "cbz",
	"application/epub+zip":          "epub",
	"application/pdf":               "pdf",
}

const (
	relAcquisition = "http://opds-spec.org/acquisition"
	relThumbnail   = "http://opds-spec.org/image/thumbnail"
	relImage       = "http://opds-spec.org/image"
	maxFeed        = 8 << 20 // a feed page
	maxBook        = 2 << 30 // a book's file
	maxPages       = 50      // pages of one series' feed
)

type feed struct {
	Links   []link  `xml:"link"`
	Entries []entry `xml:"entry"`
}

type entry struct {
	ID      string `xml:"id"`
	Title   string `xml:"title"`
	Author  string `xml:"author>name"`
	Summary string `xml:"summary"`
	Content string `xml:"content"`
	Links   []link `xml:"link"`
}

type link struct {
	Rel    string `xml:"rel,attr"`
	Href   string `xml:"href,attr"`
	Type   string `xml:"type,attr"`
	Length int64  `xml:"length,attr"`
}

type openSearch struct {
	URLs []struct {
		Type     string `xml:"type,attr"`
		Template string `xml:"template,attr"`
	} `xml:"Url"`
}

// Search finds series and books matching q, through the catalogue's own
// search (a template in the root feed, or an OpenSearch description).
func (c Catalogue) Search(ctx core.Context, q string) ([]Entry, error) {
	q = core.Trim(q)
	if q == "" {
		return nil, nil
	}
	tmpl, err := c.searchTemplate(ctx)
	if err != nil {
		return nil, err
	}
	// Escaped for where it lands: a query (Komga's ?search=) or a path (Calibre's /search/…).
	escaped := core.URLPathEscape(q)
	if i := core.Index(tmpl, "?"); i >= 0 && i < core.Index(tmpl, "{searchTerms}") {
		escaped = core.URLEncode(q)
	}
	target := core.Replace(tmpl, "{searchTerms}", escaped)
	var f feed
	if err := c.get(ctx, target, maxFeed, &f); err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, e := range f.Entries {
		if x := c.entry(target, e); x.Feed != "" || len(x.Books) > 0 {
			out = append(out, x)
		}
	}
	return out, nil
}

// Books is every book in a series' feed, following its pages.
func (c Catalogue) Books(ctx core.Context, feedURL string) ([]Book, error) {
	var out []Book
	next := feedURL
	for page := 0; next != "" && page < maxPages; page++ {
		var f feed
		if err := c.get(ctx, next, maxFeed, &f); err != nil {
			return out, err
		}
		for _, e := range f.Entries {
			out = append(out, c.entry(next, e).Books...)
		}
		here := next
		next = ""
		for _, l := range f.Links {
			if l.Rel == "next" {
				next = resolve(here, l.Href)
			}
		}
	}
	return out, nil
}

// Fetch writes a book's file into w (at most 2 GB).
func (c Catalogue) Fetch(ctx core.Context, file string, w core.Writer) error {
	resp, err := c.do(ctx, file)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if r := core.Copy(w, core.LimitReader(resp.Body, maxBook)); !r.OK {
		return core.E("opds.Fetch", "download "+file, r.Err())
	}
	return nil
}

func (c Catalogue) searchTemplate(ctx core.Context) (string, error) {
	var root feed
	if err := c.get(ctx, c.URL, maxFeed, &root); err != nil {
		return "", err
	}
	for _, l := range root.Links {
		if l.Rel != "search" {
			continue
		}
		href := resolve(c.URL, l.Href)
		if core.Contains(l.Href, "{searchTerms}") {
			return resolveTemplate(c.URL, l.Href), nil
		}
		if core.Contains(l.Type, "opensearchdescription") {
			var osd openSearch
			if err := c.get(ctx, href, 1<<20, &osd); err != nil {
				return "", err
			}
			for _, u := range osd.URLs {
				if core.Contains(u.Type, "atom") && core.Contains(u.Template, "{searchTerms}") {
					return resolveTemplate(href, u.Template), nil
				}
			}
		}
	}
	return "", core.E("opds.Search", "this catalogue can't be searched", nil)
}

// entry turns an Atom entry into a result, with every link made absolute.
func (c Catalogue) entry(base string, e entry) Entry {
	x := Entry{ID: e.ID, Title: core.Trim(e.Title), Author: core.Trim(e.Author), Summary: core.Trim(e.Summary)}
	if x.Summary == "" {
		x.Summary = core.Trim(e.Content)
	}
	for _, l := range e.Links {
		href := resolve(base, l.Href)
		switch {
		case l.Rel == relThumbnail || (l.Rel == relImage && x.Cover == ""):
			x.Cover = href
		case l.Rel == relAcquisition || core.HasPrefix(l.Rel, relAcquisition+"/open-access"):
			if f := formats[core.Lower(core.Trim(core.SplitN(l.Type, ";", 2)[0]))]; f != "" {
				x.Books = append(x.Books, Book{Title: x.Title, File: href, Format: f, Size: l.Length})
			}
		case (l.Rel == "subsection" || l.Rel == "" || l.Rel == "alternate") && core.Contains(l.Type, "opds-catalog"):
			x.Feed = href
		}
	}
	return x
}

// get fetches and parses one XML document.
func (c Catalogue) get(ctx core.Context, target string, limit int64, out any) error {
	resp, err := c.do(ctx, target)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := xml.NewDecoder(core.LimitReader(resp.Body, limit)).Decode(out); err != nil {
		return core.E("opds", "the catalogue sent something that isn't OPDS", err)
	}
	return nil
}

// do sends a GET: http(s) only, and her login only to the catalogue's host.
func (c Catalogue) do(ctx core.Context, target string) (*core.Response, error) {
	u, ok := parseHTTP(target)
	if !ok {
		return nil, core.E("opds", "not a web link: "+target, nil)
	}
	req := core.NewHTTPRequestContext(ctx, "GET", target, nil)
	if !req.OK {
		return nil, req.Err()
	}
	r := req.Value.(*core.Request)
	r.Header.Set("User-Agent", core.Coalesce(c.Agent, "Pinku (+https://github.com/Snider/manga)"))
	r.Header.Set("Accept", "application/atom+xml, application/xml, */*")
	if root, ok := parseHTTP(c.URL); ok && c.User != "" && core.EqualFold(u.Host, root.Host) && u.Scheme == root.Scheme {
		r.SetBasicAuth(c.User, c.Password)
	}
	client := c.HTTP
	if client == nil {
		client = &core.HTTPClient{Timeout: 60 * core.Second}
	}
	resp, err := client.Do(r)
	if err != nil {
		return nil, core.E("opds", "couldn't reach the catalogue", err)
	}
	if resp.StatusCode == core.StatusUnauthorized || resp.StatusCode == core.StatusForbidden {
		resp.Body.Close()
		return nil, core.E("opds", "the catalogue wants a username and password (or they're wrong)", nil)
	}
	if resp.StatusCode != core.StatusOK {
		resp.Body.Close()
		return nil, core.E("opds", "the catalogue answered "+resp.Status, nil)
	}
	return resp, nil
}

func parseHTTP(s string) (*core.URL, bool) {
	r := core.URLParse(s)
	if !r.OK {
		return nil, false
	}
	u := r.Value.(*core.URL)
	return u, (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// resolve makes href absolute against the page it was on.
func resolve(base, href string) string {
	b, okB := parseHTTP(base)
	r := core.URLParse(href)
	if !okB || !r.OK {
		return href
	}
	return b.ResolveReference(r.Value.(*core.URL)).String()
}

// resolveTemplate is resolve for a search template: making a path absolute
// escapes its {braces}, so the placeholder is shielded while it's resolved.
func resolveTemplate(base, tmpl string) string {
	const shield = "pinkusearchterms0"
	return core.Replace(resolve(base, core.Replace(tmpl, "{searchTerms}", shield)), shield, "{searchTerms}")
}
