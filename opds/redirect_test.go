// SPDX-License-Identifier: EUPL-1.2

package opds

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Redirects: followed on the web (and kept home when asked), never to file:,
// never forever.
func TestRedirects(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("from elsewhere")) }))
	defer elsewhere.Close()
	var home *httptest.Server
	home = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, elsewhere.URL+"/x", http.StatusFound)
		case "/file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, home.URL+"/loop", http.StatusFound)
		case "/here":
			http.Redirect(w, r, home.URL+"/ok", http.StatusFound)
		case "/ok":
			w.Write([]byte("ok"))
		}
	}))
	defer home.Close()
	ctx := context.Background()
	var b bytes.Buffer
	if err := (Catalogue{URL: home.URL}).Fetch(ctx, home.URL+"/away", &b); err != nil || b.String() != "from elsewhere" {
		t.Errorf("an ordinary redirect elsewhere: %q %v", b.String(), err)
	}
	b.Reset()
	if err := (Catalogue{URL: home.URL, StayHome: true}).Fetch(ctx, home.URL+"/away", &b); err == nil {
		t.Error("StayHome followed a redirect off the server")
	}
	if err := (Catalogue{URL: home.URL, StayHome: true}).Fetch(ctx, home.URL+"/here", &b); err != nil || b.String() != "ok" {
		t.Errorf("StayHome refused a redirect on the same server: %q %v", b.String(), err)
	}
	for _, path := range []string{"/file", "/loop"} {
		if err := (Catalogue{URL: home.URL}).Fetch(ctx, home.URL+path, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: followed", path)
		}
	}
}
