// SPDX-License-Identifier: EUPL-1.2

package main

import (
	core "github.com/dappcore/go"

	"manga/shelf"
)

// version is this build's release ("v0.4.1"); the release workflow stamps it
// with -ldflags "-X main.version=…". A local build is "dev" and never nags.
var version = "dev"

// releasesURL lists Pinku's releases. Every v0.x is a pre-release, which
// GitHub's /releases/latest skips, so the list it is. (Tests point it elsewhere.)
var releasesURL = "https://api.github.com/repos/Snider/manga/releases?per_page=5"

// releaseCheck is what /api/version answers: this build, the newest release, and
// whether that's newer.
type releaseCheck struct {
	Version string `json:"version"`
	Latest  string `json:"latest,omitempty"`
	URL     string `json:"url,omitempty"`
	Newer   bool   `json:"newer"`
}

// checkUpdate asks GitHub at most once a day (the answer is kept in the
// shelf), so opening Pinku twenty times sends one request. Nothing about her
// goes with it: it's a plain GET of a public list.
func checkUpdate(ctx core.Context, sh *shelf.Shelf, now core.Time) releaseCheck {
	u := releaseCheck{Version: version}
	if version == "dev" {
		return u
	}
	latest, url := sh.Noted("latest"), sh.Noted("latest_url")
	checked, _ := core.ParseInt(sh.Noted("latest_checked"), 10, 64).Value.(int64)
	if latest == "" || now.Unix()-checked > 24*3600 {
		if tag, page, err := newestRelease(ctx); err == nil {
			latest, url = tag, page
			sh.Note("latest", tag)
			sh.Note("latest_url", page)
		}
		sh.Note("latest_checked", core.FormatInt(now.Unix(), 10)) // a failed check waits a day too
	}
	u.Latest, u.URL = latest, url
	u.Newer = latest != "" && newerVersion(latest, version)
	return u
}

// newestRelease is the newest published (not draft) release's tag and page.
func newestRelease(ctx core.Context) (string, string, error) {
	ctx, cancel := core.WithTimeout(ctx, 10*core.Second)
	defer cancel()
	req := core.NewHTTPRequestContext(ctx, "GET", releasesURL, nil)
	if !req.OK {
		return "", "", req.Err()
	}
	r := req.Value.(*core.Request)
	r.Header.Set("User-Agent", "Pinku/"+version+" (+https://github.com/Snider/manga)")
	r.Header.Set("Accept", "application/vnd.github+json")
	resp, err := new(core.HTTPClient).Do(r)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != core.StatusOK {
		return "", "", core.E("pinku.updates", "GitHub answered "+resp.Status, nil)
	}
	var releases []struct {
		Tag   string `json:"tag_name"`
		URL   string `json:"html_url"`
		Draft bool   `json:"draft"`
	}
	if err := core.JSONNewDecoder(core.LimitReader(resp.Body, 1<<20)).Decode(&releases); err != nil {
		return "", "", err
	}
	best, page := "", ""
	for _, rel := range releases {
		if rel.Draft || !core.HasPrefix(rel.URL, "https://github.com/") {
			continue
		}
		if best == "" || newerVersion(rel.Tag, best) {
			best, page = rel.Tag, rel.URL
		}
	}
	if best == "" {
		return "", "", core.E("pinku.updates", "no releases listed", nil)
	}
	return best, page, nil
}

// newerVersion: is a (v1.2.3) newer than b? Anything that isn't vX.Y.Z is never newer.
func newerVersion(a, b string) bool {
	pa, okA := semver(a)
	pb, okB := semver(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func semver(v string) ([3]int, bool) {
	var out [3]int
	parts := core.Split(core.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n := core.Atoi(p)
		if !n.OK {
			return out, false
		}
		out[i] = n.Value.(int)
	}
	return out, true
}
