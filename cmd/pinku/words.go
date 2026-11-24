// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"embed"

	core "github.com/dappcore/go"
	i18n "github.com/dappcore/go-i18n/go"
)

// Pinku's own words: locales/en.json is the source, other languages are the
// same keys (anything missing falls back to English). go-i18n loads them on
// top of its grammar tables, so the language Windows is set to comes for free.
//
//go:embed locales/*.json
var localeFiles embed.FS

// words holds the translations and answers /api/strings.
type words struct {
	mu    core.Mutex
	svc   *i18n.Service
	keys  []string // every ui.* key, from en.json
	langs []string // the languages Pinku has words for ("en", "tr")
	auto  string   // what "same as Windows" means here
}

func newWords() (*words, error) {
	r := i18n.New()
	if !r.OK {
		return nil, r.Err()
	}
	svc := r.Value.(*i18n.Service)
	if loaded := svc.AddLoader(i18n.NewFSLoader(localeFiles, "locales")); !loaded.OK {
		return nil, loaded.Err()
	}
	src, err := localeFiles.ReadFile("locales/en.json")
	if err != nil {
		return nil, core.E("pinku.words", "read en.json", err)
	}
	var tree map[string]any
	if parsed := core.JSONUnmarshal(src, &tree); !parsed.OK {
		return nil, core.E("pinku.words", "en.json", parsed.Err())
	}
	w := &words{svc: svc, keys: flatten("", tree)}
	entries, err := localeFiles.ReadDir("locales")
	if err != nil {
		return nil, core.E("pinku.words", "list locales", err)
	}
	for _, e := range entries {
		// Hidden files aren't languages (macOS's "._en.json"; go-i18n skips them too).
		if e.IsDir() || core.HasPrefix(e.Name(), ".") || !core.HasSuffix(e.Name(), ".json") {
			continue
		}
		w.langs = append(w.langs, core.TrimSuffix(e.Name(), ".json"))
	}
	w.auto = w.pick(svc.Language()) // detected from Windows (or LANG) when go-i18n started
	return w, nil
}

// flatten turns {"ui": {"tab": {"shelf": "Bookcase"}}} into "ui.tab.shelf".
func flatten(prefix string, tree map[string]any) []string {
	var out []string
	for k, v := range tree {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok {
			out = append(out, flatten(key, sub)...)
			continue
		}
		out = append(out, key)
	}
	core.SliceSort(out)
	return out
}

// pick is lang if Pinku has words for it ("tr-TR" counts as "tr"), else English.
func (w *words) pick(lang string) string {
	base := core.Lower(lang)
	if i := core.Index(base, "-"); i > 0 {
		base = base[:i]
	}
	for _, l := range w.langs {
		if l == base {
			return l
		}
	}
	return "en"
}

// For is every ui.* string in lang ("" = same as Windows), and the language used.
func (w *words) For(lang string) (string, map[string]string) {
	use := w.auto
	if lang != "" {
		use = w.pick(lang)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.svc.SetLanguage(use)
	out := make(map[string]string, len(w.keys))
	for _, k := range w.keys {
		out[k] = w.svc.T(k)
	}
	return use, out
}
