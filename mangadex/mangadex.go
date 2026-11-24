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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
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

var coverFile = regexp.MustCompile(`^[0-9a-f-]{36}\.(jpg|jpeg|png|gif|webp)$`)

// Cover fetches a small cover image (the page shows covers through the app,
// not straight from MangaDex, so the window never fetches from elsewhere).
func (c *Client) Cover(ctx context.Context, id, file string) ([]byte, error) {
	if ID(id) != id || !coverFile.MatchString(file) {
		return nil, fmt.Errorf("not a MangaDex cover")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.UploadsURL+"/covers/"+id+"/"+file+".256.jpg", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.Agent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// Client talks to the MangaDex API.
type Client struct {
	BaseURL    string // https://api.mangadex.org (tests point these elsewhere)
	UploadsURL string // https://uploads.mangadex.org
	HTTP       *http.Client
	Agent      string // MangaDex asks clients to say who they are
}

// New is a Client for the real API.
func New() *Client {
	return &Client{BaseURL: "https://api.mangadex.org", UploadsURL: "https://uploads.mangadex.org", HTTP: &http.Client{Timeout: 20 * time.Second}, Agent: "Pinku/0.4 (+https://github.com/Snider/manga)"}
}

// Search finds manga by title, best match first. lang ("en") only orders the
// results: ones translated into it come first. Adult titles are left out.
func (c *Client) Search(ctx context.Context, title, lang string) ([]Manga, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil
	}
	q := url.Values{}
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
	if lang != "" {
		sort.SliceStable(out, func(i, j int) bool { return has(out[i].Languages, lang) && !has(out[j].Languages, lang) })
	}
	return out, nil
}

// Get is one manga by its id (or its MangaDex URL).
func (c *Client) Get(ctx context.Context, idOrURL string) (Manga, error) {
	id := ID(idOrURL)
	if id == "" {
		return Manga{}, fmt.Errorf("that isn't a MangaDex link or id: %q", idOrURL)
	}
	var body struct {
		Data item `json:"data"`
	}
	if err := c.get(ctx, "/manga/"+id+"?includes[]=cover_art&includes[]=author", &body); err != nil {
		return Manga{}, err
	}
	return body.Data.flatten(), nil
}

var idPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// ID pulls the manga id out of a MangaDex link (or returns a bare id).
//
//	mangadex.ID("https://mangadex.org/title/cbe84ea7-2992-4db0-b4ef-d042124f48c8/uso") // "cbe84ea7-…"
func ID(s string) string {
	return idPattern.FindString(strings.ToLower(s))
}

// URL is the MangaDex page for an id.
func URL(id string) string { return "https://mangadex.org/title/" + id }

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.Agent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach MangaDex: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("MangaDex doesn't have that one")
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("MangaDex says slow down: try again in a minute")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("MangaDex answered %s", resp.Status)
	}
	return json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 8<<20)).Decode(out)
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
	if i := strings.Index(m.Description, "\n---"); i > 0 { // the links/credits block MangaDex descriptions end with
		m.Description = strings.TrimSpace(m.Description[:i])
	}
	for _, t := range a.Tags {
		if t.Attributes.Group == "genre" || t.Attributes.Group == "theme" {
			if n := t.Attributes.Name["en"]; n != "" {
				m.Tags = append(m.Tags, n)
			}
		}
	}
	sort.Strings(m.Tags)
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
	sort.Strings(keys)
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
