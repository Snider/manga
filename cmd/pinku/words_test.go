// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
)

// Every language has every key en.json has (no half-translated window), and
// Turkish really is Turkish.
func TestWordsTurkish(t *testing.T) {
	w, err := newWords()
	if err != nil {
		t.Fatal(err)
	}
	lang, tr := w.For("tr")
	if lang != "tr" || tr["ui.tab.shelf"] != "Kitaplık" || tr["ui.tab.jobs"] != "İndirilenler" {
		t.Fatalf("tr: %s %q %q", lang, tr["ui.tab.shelf"], tr["ui.tab.jobs"])
	}
	_, en := w.For("en")
	if en["ui.tab.shelf"] != "Bookcase" {
		t.Fatalf("en: %q", en["ui.tab.shelf"])
	}
	for _, k := range w.keys {
		if tr[k] == "" || tr[k] == k {
			t.Errorf("tr has no %s", k)
		}
		if strings.Contains(en[k], "{{.") != strings.Contains(tr[k], "{{.") {
			t.Errorf("%s: placeholders differ: %q / %q", k, en[k], tr[k])
		}
	}
}

// A language Pinku has no words for gets English; a regional tag gets its base.
func TestWordsPick(t *testing.T) {
	w, err := newWords()
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"tr-TR": "tr", "de": "en", "": "en", "EN-gb": "en"} {
		if got := w.pick(in); got != want {
			t.Errorf("pick(%q) = %q, want %q", in, got, want)
		}
	}
}
