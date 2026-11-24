// SPDX-License-Identifier: EUPL-1.2

// Package mangadex searches MangaDex (api.mangadex.org) for the "Find manga"
// page. Downloading is mangadex-dl's job (package downloader); this only finds
// things and shows their covers.
//
//	c := mangadex.New()
//	found, err := c.Search(ctx, "yotsuba", "en")
//	found[0].Title      // "Yotsuba&!"
//	found[0].CoverURL() // https://uploads.mangadex.org/covers/<id>/<file>.256.jpg
package mangadex

import (
	core "github.com/dappcore/go"
)

// Manga is one search result, flattened for the page.
type Manga struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	AltTitle    string   `json:"altTitle,omitempty"` // the romanised one, if Title is English
	Description string   `json:"description"`
	Status      string   `json:"status"` // ongoing, completed, hiatus, cancelled
	Year        int      `json:"year,omitempty"`
	LastVolume  string   `json:"lastVolume,omitempty"`
	LastChapter string   `json:"lastChapter,omitempty"`
	Tags        []string `json:"tags"`
	Languages   []string `json:"languages"` // what it's translated into
	Authors     []string `json:"authors"`
	CoverFile   string   `json:"-"`
	Cover       string   `json:"cover"` // the cover through the app: /api/dexcover/<id>/<file> ("" when it has none)
	URL         string   `json:"url"`   // the MangaDex page, which is what mangadex-dl takes
}

// CoverURL is the small cover image ("" if MangaDex has none).
func (m Manga) CoverURL() string {
	if m.CoverFile == "" {
		return ""
	}
	return "https://uploads.mangadex.org/covers/" + m.ID + "/" + m.CoverFile + ".256.jpg"
}

var coverFile = core.MustRegex(`^[0-9a-f-]{36}\.(jpg|jpeg|png|gif|webp)$`)

// Cover fetches a small cover image (the page shows covers through the app,
// not straight from MangaDex, so the window never fetches from elsewhere).
func (c *Client) Cover(ctx core.Context, id, file string) ([]byte, error) {
	if ID(id) != id || !coverFile.MatchString(file) {
		return nil, core.E("mangadex.Cover", "not a MangaDex cover", nil)
	}
	resp, err := c.do(ctx, c.UploadsURL+"/covers/"+id+"/"+file+".256.jpg")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != core.StatusOK {
		resp.Body.Close()
		return nil, core.E("mangadex.Cover", "cover: "+resp.Status, nil)
	}
	got := core.ReadAll(core.LimitReader(resp.Body, 4<<20)) // closes the body
	if !got.OK {
		return nil, core.E("mangadex.Cover", "read the cover", got.Err())
	}
	return []byte(got.Value.(string)), nil
}

// Client talks to the MangaDex API.
type Client struct {
	BaseURL    string // https://api.mangadex.org (tests point these elsewhere)
	UploadsURL string // https://uploads.mangadex.org
	HTTP       *core.HTTPClient
	Agent      string // MangaDex asks clients to say who they are
}

// New is a Client for the real API.
func New() *Client {
	return &Client{BaseURL: "https://api.mangadex.org", UploadsURL: "https://uploads.mangadex.org", HTTP: &core.HTTPClient{Timeout: 20 * core.Second}, Agent: "Pinku/0.4 (+https://github.com/Snider/manga)"}
}

// Search finds manga by title, best match first. lang ("en") only orders the
// results: ones translated into it come first. Adult titles are left out.
func (c *Client) Search(ctx core.Context, title, lang string) ([]Manga, error) {
	title = core.Trim(title)
	if title == "" {
		return nil, nil
	}
	q := core.URLValues{}
	q.Set("title", title)
	q.Set("limit", "24")
	q.Add("includes[]", "cover_art")
	q.Add("includes[]", "author")
	q.Add("contentRating[]", "safe")
	q.Add("contentRating[]", "suggestive")
	q.Set("order[relevance]", "desc")
	var body struct {
		Result string `json:"result"`
		Data   []item `json:"data"`
		Errors []struct {
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if err := c.get(ctx, "/manga?"+q.Encode(), &body); err != nil {
		return nil, err
	}
	out := make([]Manga, 0, len(body.Data))
	for _, it := range body.Data {
		out = append(out, it.flatten())
	}
	if lang == "" {
		return out, nil
	}
	// Hers first, each group keeping MangaDex's relevance order.
	theirs := make([]Manga, 0, len(out))
	hers := out[:0:0]
	for _, m := range out {
		if has(m.Languages, lang) {
			hers = append(hers, m)
		} else {
			theirs = append(theirs, m)
		}
	}
	return append(hers, theirs...), nil
}

// Get is one manga by its id (or its MangaDex URL).
func (c *Client) Get(ctx core.Context, idOrURL string) (Manga, error) {
	id := ID(idOrURL)
	if id == "" {
		return Manga{}, core.E("mangadex.Get", core.Sprintf("that isn't a MangaDex link or id: %q", idOrURL), nil)
	}
	var body struct {
		Data item `json:"data"`
	}
	if err := c.get(ctx, "/manga/"+id+"?includes[]=cover_art&includes[]=author", &body); err != nil {
		return Manga{}, err
	}
	return body.Data.flatten(), nil
}

var idPattern = core.MustRegex(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// ID pulls the manga id out of a MangaDex link (or returns a bare id).
//
//	mangadex.ID("https://mangadex.org/title/cbe84ea7-2992-4db0-b4ef-d042124f48c8/uso") // "cbe84ea7-…"
func ID(s string) string {
	return idPattern.FindString(core.Lower(s))
}

// URL is the MangaDex page for an id.
func URL(id string) string { return "https://mangadex.org/title/" + id }

// do sends a GET with Pinku's User-Agent (MangaDex asks clients to say who they are).
func (c *Client) do(ctx core.Context, target string) (*core.Response, error) {
	req := core.NewHTTPRequestContext(ctx, "GET", target, nil)
	if !req.OK {
		return nil, req.Err()
	}
	r := req.Value.(*core.Request)
	r.Header.Set("User-Agent", c.Agent)
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, core.E("mangadex", "couldn't reach MangaDex", err)
	}
	return resp, nil
}

func (c *Client) get(ctx core.Context, path string, out any) error {
	resp, err := c.do(ctx, c.BaseURL+path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == core.StatusNotFound:
		return core.E("mangadex", "MangaDex doesn't have that one", nil)
	case resp.StatusCode == core.StatusTooManyRequests:
		return core.E("mangadex", "MangaDex says slow down: try again in a minute", nil)
	case resp.StatusCode != core.StatusOK:
		return core.E("mangadex", "MangaDex answered "+resp.Status, nil)
	}
	return core.JSONNewDecoder(core.LimitReader(resp.Body, 8<<20)).Decode(out)
}

type item struct {
	ID         string `json:"id"`
	Attributes struct {
		Title       map[string]string   `json:"title"`
		AltTitles   []map[string]string `json:"altTitles"`
		Description map[string]string   `json:"description"`
		Status      string              `json:"status"`
		Year        int                 `json:"year"`
		LastVolume  string              `json:"lastVolume"`
		LastChapter string              `json:"lastChapter"`
		Languages   []string            `json:"availableTranslatedLanguages"`
		Tags        []struct {
			Attributes struct {
				Name  map[string]string `json:"name"`
				Group string            `json:"group"`
			} `json:"attributes"`
		} `json:"tags"`
	} `json:"attributes"`
	Relationships []struct {
		Type       string `json:"type"`
		Attributes struct {
			FileName string `json:"fileName"`
			Name     string `json:"name"`
		} `json:"attributes"`
	} `json:"relationships"`
}

func (it item) flatten() Manga {
	a := it.Attributes
	m := Manga{ID: it.ID, Status: a.Status, Year: a.Year, LastVolume: a.LastVolume, LastChapter: a.LastChapter, Languages: a.Languages, URL: URL(it.ID)}
	// English title if it has one (main or alternative), else whatever the main one is.
	main := first(a.Title)
	m.Title = a.Title["en"]
	if m.Title == "" {
		for _, alt := range a.AltTitles {
			if t := alt["en"]; t != "" {
				m.Title = t
				break
			}
		}
	}
	if m.Title == "" {
		m.Title = main
	}
	if main != m.Title {
		m.AltTitle = main
	}
	m.Description = a.Description["en"]
	if m.Description == "" {
		m.Description = first(a.Description)
	}
	if i := core.Index(m.Description, "\n---"); i > 0 { // the links/credits block MangaDex descriptions end with
		m.Description = core.Trim(m.Description[:i])
	}
	for _, t := range a.Tags {
		if t.Attributes.Group == "genre" || t.Attributes.Group == "theme" {
			if n := t.Attributes.Name["en"]; n != "" {
				m.Tags = append(m.Tags, n)
			}
		}
	}
	core.SliceSort(m.Tags)
	for _, r := range it.Relationships {
		switch r.Type {
		case "cover_art":
			m.CoverFile = r.Attributes.FileName
		case "author":
			if r.Attributes.Name != "" && !has(m.Authors, r.Attributes.Name) {
				m.Authors = append(m.Authors, r.Attributes.Name)
			}
		}
	}
	if m.CoverFile != "" {
		m.Cover = "/api/dexcover/" + m.ID + "/" + m.CoverFile
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.Languages == nil {
		m.Languages = []string{}
	}
	if m.Authors == nil {
		m.Authors = []string{}
	}
	return m
}

// first is the value of the one-entry title maps MangaDex uses ({"ja-ro": "…"}).
func first(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	core.SliceSort(keys)
	for _, k := range keys {
		if m[k] != "" {
			return m[k]
		}
	}
	return ""
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
