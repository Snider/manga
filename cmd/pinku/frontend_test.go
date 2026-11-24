// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"regexp"
	"testing"

	"manga/shelf"
)

// Every key the page asks for exists, in every language: a typo in app.js or
// index.html would otherwise ship as a raw "ui.something" on her screen.
func TestPageKeysExist(t *testing.T) {
	w, err := newWords()
	if err != nil {
		t.Fatal(err)
	}
	js, _ := frontend.ReadFile("frontend/app.js")
	html, _ := frontend.ReadFile("frontend/index.html")
	// Comments show example keys ("ui.x"): read code only.
	js = regexp.MustCompile(`(?m)^\s*//.*$`).ReplaceAll(js, nil)
	need := map[string]string{} // key → where it's used
	for _, m := range regexp.MustCompile(`\bt\("(ui\.[\w.-]*[A-Za-z0-9])"`).FindAllStringSubmatch(string(js), -1) {
		need[m[1]] = "app.js t()"
	}
	for _, m := range regexp.MustCompile(`\btn\("(ui\.[\w.-]*[A-Za-z0-9])"`).FindAllStringSubmatch(string(js), -1) {
		need[m[1]+"_one"], need[m[1]+"_other"] = "app.js tn()", "app.js tn()"
	}
	for _, m := range regexp.MustCompile(`data-i18n(?:-placeholder|-title)?="(ui\.[\w.-]+)"`).FindAllStringSubmatch(string(html)+string(js), -1) {
		need[m[1]] = "data-i18n"
	}
	// Keys the page builds at run time ("ui.status." + status and so on).
	for _, s := range []string{"ongoing", "completed", "hiatus", "cancelled"} { // MangaDex's statuses
		need["ui.status."+s] = "app.js status pill"
	}
	for _, m := range regexp.MustCompile(`<option value="([\w-]+)" data-i18n="ui\.lang\.`).FindAllStringSubmatch(string(html), -1) {
		need["ui.lang."+m[1]] = "download language list"
	}
	for _, l := range w.langs {
		need["ui.lang."+l] = "Pinku's language list"
	}
	for f := range shelf.Formats {
		need["ui.settings.format_"+f] = "file formats"
	}
	if len(need) < 100 {
		t.Fatalf("only found %d keys: the patterns above have stopped matching the page", len(need))
	}
	for _, lang := range w.langs {
		_, text := w.For(lang)
		for k, where := range need {
			if text[k] == "" || text[k] == k {
				t.Errorf("%s: no %s (used by %s)", lang, k, where)
			}
		}
	}
}
