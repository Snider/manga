// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"manga/shelf"
)

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.4.2", "v0.4.1", true}, {"v0.10.0", "v0.9.9", true}, {"v1.0.0", "v0.99.0", true},
		{"v0.4.1", "v0.4.1", false}, {"v0.4.0", "v0.4.1", false},
		{"dev", "v0.4.1", false}, {"v0.4.2", "dev", false}, {"v0.4", "v0.3.0", false}, {"vX.1.2", "v0.0.1", false},
	} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// The newest release wins whatever order GitHub lists them in, drafts are
// skipped, and the answer is kept for a day: the second check asks nobody.
func TestCheckUpdate(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`[{"tag_name":"v0.6.0","html_url":"https://github.com/Snider/manga/releases/tag/v0.6.0","draft":true},
			{"tag_name":"v0.4.1","html_url":"https://github.com/Snider/manga/releases/tag/v0.4.1"},
			{"tag_name":"v0.5.0","html_url":"https://github.com/Snider/manga/releases/tag/v0.5.0"}]`))
	}))
	defer srv.Close()
	oldURL, oldVersion := releasesURL, version
	releasesURL, version = srv.URL, "v0.4.1"
	defer func() { releasesURL, version = oldURL, oldVersion }()
	r := shelf.Open(t.TempDir() + "/shelf.db")
	if !r.OK {
		t.Fatal(r.Error())
	}
	sh := r.Value.(*shelf.Shelf)
	t.Cleanup(func() { sh.Close() }) // Windows won't delete an open database
	now := time.Now()

	u := checkUpdate(t.Context(), sh, now)
	if !u.Newer || u.Latest != "v0.5.0" || u.URL != "https://github.com/Snider/manga/releases/tag/v0.5.0" {
		t.Fatalf("first check: %+v", u)
	}
	if checkUpdate(t.Context(), sh, now.Add(time.Hour)); calls != 1 {
		t.Fatalf("asked GitHub %d times in an hour", calls)
	}
	if checkUpdate(t.Context(), sh, now.Add(25*time.Hour)); calls != 2 {
		t.Fatalf("didn't ask again after a day (%d calls)", calls)
	}
	version = "dev"
	if u := checkUpdate(t.Context(), sh, now.Add(50*time.Hour)); u.Newer || calls != 2 {
		t.Fatalf("a dev build checked or nagged: %+v, %d calls", u, calls)
	}
}
