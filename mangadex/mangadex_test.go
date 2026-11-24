// SPDX-License-Identifier: EUPL-1.2

package mangadex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fake serves real API answers saved in testdata (search.json is a
// one-result search, yotsuba.json a two-result one).
func fake(t *testing.T, file string, status int) (*Client, *http.Request) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		w.WriteHeader(status)
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	c := New()
	c.BaseURL = srv.URL
	return c, &got
}

func TestSearchFlattensARealAnswer(t *testing.T) {
	c, req := fake(t, "search.json", 200)
	found, err := c.Search(context.Background(), "uso-douri", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found %d, want 1", len(found))
	}
	m := found[0]
	if m.ID != "cbe84ea7-2992-4db0-b4ef-d042124f48c8" || m.Title != "Uso-douri no Kottouya" || m.AltTitle != "" {
		t.Errorf("got %+v", m)
	}
	if m.Cover != "/api/dexcover/cbe84ea7-2992-4db0-b4ef-d042124f48c8/b899141b-3a87-4871-95f6-5fdfd93b68e0.jpg" || m.CoverURL() != "https://uploads.mangadex.org/covers/cbe84ea7-2992-4db0-b4ef-d042124f48c8/b899141b-3a87-4871-95f6-5fdfd93b68e0.jpg.256.jpg" {
		t.Errorf("cover %q", m.Cover)
	}
	if strings.Join(m.Authors, ",") != "Kotegawa Yua,Muroyoshi Takashi" {
		t.Errorf("authors %v", m.Authors)
	}
	if m.URL != "https://mangadex.org/title/cbe84ea7-2992-4db0-b4ef-d042124f48c8" {
		t.Errorf("url %q", m.URL)
	}
	if ua := req.Header.Get("User-Agent"); !strings.HasPrefix(ua, "Pinku/") {
		t.Errorf("user agent %q", ua)
	}
	if q := req.URL.Query(); q.Get("title") != "uso-douri" || len(q["contentRating[]"]) != 2 {
		t.Errorf("query %v", q)
	}
}

func TestSearchPutsYourLanguageFirst(t *testing.T) {
	c, _ := fake(t, "yotsuba.json", 200)
	found, err := c.Search(context.Background(), "yotsuba", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "313682fc-c512-4a1c-8c66-33ad0121c505" {
		t.Fatalf("the English one should come first: %+v", found)
	}
	other := found[1]
	if other.Title == "" || len(other.Tags) == 0 || other.Description == "" {
		t.Errorf("flattened badly: %+v", other)
	}
	if len(other.Languages) != 0 {
		t.Errorf("languages %v", other.Languages)
	}
}

func TestSearchErrors(t *testing.T) {
	c, _ := fake(t, "search.json", 429)
	if _, err := c.Search(context.Background(), "x", ""); err == nil || !strings.Contains(err.Error(), "slow down") {
		t.Errorf("429: %v", err)
	}
	if found, err := c.Search(context.Background(), "   ", ""); found != nil || err != nil {
		t.Errorf("empty search should do nothing: %v %v", found, err)
	}
	if _, err := c.Get(context.Background(), "not a link"); err == nil {
		t.Error("Get of junk should fail")
	}
}

func TestID(t *testing.T) {
	for in, want := range map[string]string{
		"https://mangadex.org/title/cbe84ea7-2992-4db0-b4ef-d042124f48c8/uso-douri-no-kottouya": "cbe84ea7-2992-4db0-b4ef-d042124f48c8",
		"CBE84EA7-2992-4DB0-B4EF-D042124F48C8":                                                  "cbe84ea7-2992-4db0-b4ef-d042124f48c8",
		"https://mangadex.org/":                                                                 "",
	} {
		if got := ID(in); got != want {
			t.Errorf("ID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCoverOnlyFetchesCovers(t *testing.T) {
	c, req := fake(t, "search.json", 200)
	c.UploadsURL = c.BaseURL
	b, err := c.Cover(context.Background(), "cbe84ea7-2992-4db0-b4ef-d042124f48c8", "b899141b-3a87-4871-95f6-5fdfd93b68e0.jpg")
	if err != nil || len(b) == 0 || req.URL.Path != "/covers/cbe84ea7-2992-4db0-b4ef-d042124f48c8/b899141b-3a87-4871-95f6-5fdfd93b68e0.jpg.256.jpg" {
		t.Errorf("cover: %d bytes, %v, path %s", len(b), err, req.URL.Path)
	}
	for _, bad := range [][2]string{{"../x", "b899141b-3a87-4871-95f6-5fdfd93b68e0.jpg"}, {"cbe84ea7-2992-4db0-b4ef-d042124f48c8", "../../etc.jpg"}, {"cbe84ea7-2992-4db0-b4ef-d042124f48c8", "b899141b-3a87-4871-95f6-5fdfd93b68e0.jpg?x=1"}} {
		if _, err := c.Cover(context.Background(), bad[0], bad[1]); err == nil {
			t.Errorf("Cover(%q, %q) should fail", bad[0], bad[1])
		}
	}
}

// Get by id or link; MangaDex's refusals and outages come back as errors she
// can read, not a crash or an empty result.
func TestGet(t *testing.T) {
	c, req := fake(t, "manga.json", 200) // one entity: the first of yotsuba.json, as /manga/{id} answers
	if _, err := c.Get(context.Background(), "not a link"); err == nil {
		t.Error("Get without an id")
	}
	if _, err := c.Get(context.Background(), "https://mangadex.org/title/58be6aa6-06cb-4ca5-bd20-f1392ce451fb/yotsuba"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(req.URL.Path, "/manga/58be6aa6-06cb-4ca5-bd20-f1392ce451fb") {
		t.Errorf("asked for %s", req.URL.Path)
	}
	for status, want := range map[int]string{404: "doesn't have", 429: "slow down", 500: "500"} {
		c, _ := fake(t, "yotsuba.json", status)
		if _, err := c.Get(context.Background(), "58be6aa6-06cb-4ca5-bd20-f1392ce451fb"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: %v", status, err)
		}
	}
}

// Covers that MangaDex hasn't got, or can't send, are errors; a manga with
// no cover has no cover URL.
func TestCoverFailures(t *testing.T) {
	c, _ := fake(t, "search.json", 404)
	c.UploadsURL = c.BaseURL
	if _, err := c.Cover(context.Background(), "cbe84ea7-2992-4db0-b4ef-d042124f48c8", "b899141b-3a87-4871-95f6-5fdfd93b68e0.jpg"); err == nil {
		t.Error("a 404 cover")
	}
	c.UploadsURL = "http://127.0.0.1:1"
	if _, err := c.Cover(context.Background(), "cbe84ea7-2992-4db0-b4ef-d042124f48c8", "b899141b-3a87-4871-95f6-5fdfd93b68e0.jpg"); err == nil {
		t.Error("an unreachable cover")
	}
	if (Manga{}).CoverURL() != "" {
		t.Error("a CoverURL without a cover")
	}
	if u := (Manga{ID: "x", CoverFile: "c.jpg"}).CoverURL(); u != "https://uploads.mangadex.org/covers/x/c.jpg.256.jpg" {
		t.Errorf("CoverURL = %s", u)
	}
}

// MangaDex unreachable: an error saying so. No language: MangaDex's order.
func TestSearchUnreachableAndUnordered(t *testing.T) {
	c := New()
	c.BaseURL = "http://127.0.0.1:1"
	if _, err := c.Search(context.Background(), "x", "en"); err == nil || !strings.Contains(err.Error(), "couldn't reach") {
		t.Errorf("unreachable: %v", err)
	}
	c, _ = fake(t, "search.json", 200)
	if got, err := c.Search(context.Background(), "uso", ""); err != nil || len(got) != 1 {
		t.Errorf("no language: %v %v", got, err)
	}
}

// Titles and descriptions: English if there is one (main or alternative),
// else the first; the credits block MangaDex descriptions end with is cut;
// empty lists come back empty, not null.
func TestFlattenFallbacks(t *testing.T) {
	var it item
	json.Unmarshal([]byte(`{"id":"cbe84ea7-2992-4db0-b4ef-d042124f48c8","attributes":{
		"title":{"ja-ro":"Uso-douri"},
		"altTitles":[{"ja":"嘘通り"},{"en":"As Lies Go"}],
		"description":{"fr":"Une boutique.\n---\n[Links](x)"}}}`), &it)
	m := it.flatten()
	if m.Title != "As Lies Go" || m.AltTitle != "Uso-douri" || m.Description != "Une boutique." {
		t.Errorf("flatten: %+v", m)
	}
	if m.Tags == nil || m.Languages == nil || m.Authors == nil || m.Cover != "" {
		t.Errorf("lists should be empty, not null: %+v", m)
	}
	var bare item // a fresh one: Unmarshal keeps fields the new answer doesn't mention
	json.Unmarshal([]byte(`{"id":"x","attributes":{"title":{"ja-ro":"Only Romaji"}}}`), &bare)
	if m := bare.flatten(); m.Title != "Only Romaji" || m.AltTitle != "" {
		t.Errorf("no English anywhere: %+v", m)
	}
}
