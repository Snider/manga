// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	core "github.com/dappcore/go"
)

// Line is what one line of mangadex-dl's log means for the page.
type Line struct {
	Kind string // "page", "volume", "done", "error", "" (just chatter)
	Text string // for "page": "Vol. 1 Ch. 3 · page 12"; for "error": the message
}

var (
	pageLine   = core.MustRegex(`^\[INFO\] Downloading (?:\[[^\]]*\] )?(.*?) page (\d+)$`)
	volumeLine = core.MustRegex(`^\[INFO\] (.*) has finished download`)
)

// Parse reads one line of mangadex-dl's log (it logs to stderr, one line per
// event, with --progress-bar-layout none). It exits 0 even when it fails, so
// the [ERROR] lines are how a failure is noticed.
//
//	Parse(`[INFO] Downloading [group] Volume. 1 Chapter. 1 page 2`) // {page, "Vol. 1 Ch. 1 · page 2"}
//	Parse(`[ERROR] Manga 'X' with en language has no chapters`)      // {error, …}
func Parse(raw string) Line {
	s := core.Trim(raw)
	switch {
	case core.HasPrefix(s, "[ERROR]"), core.HasPrefix(s, "[CRITICAL]"):
		return Line{Kind: "error", Text: friendly(core.Trim(s[core.Index(s, "]")+1:]))}
	case core.HasPrefix(s, "Traceback (most recent call last)"):
		return Line{Kind: "error", Text: "mangadex-dl crashed (the details are in the log)"}
	case core.HasPrefix(s, "[INFO] Download finished for manga"):
		return Line{Kind: "done"}
	}
	if m := pageLine.FindStringSubmatch(s); m != nil {
		return Line{Kind: "page", Text: shortVol(m[1]) + " · page " + m[2]}
	}
	if m := volumeLine.FindStringSubmatch(s); m != nil {
		return Line{Kind: "volume", Text: m[1]}
	}
	return Line{}
}

// friendly rewords the errors she's likely to meet.
func friendly(msg string) string {
	if core.Contains(msg, "has no chapters") {
		return "MangaDex has no chapters of this in your language. Popular licensed series are often taken down there: try another language in Settings, or another series."
	}
	return msg
}

// shortVol: "Volume. 1" → "Vol. 1", "Chapter. 3" → "Ch. 3".
func shortVol(s string) string {
	return core.Replace(core.Replace(s, "Volume. ", "Vol. "), "Chapter. ", "Ch. ")
}
