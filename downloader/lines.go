// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"regexp"
	"strings"
)

// Line is what one line of mangadex-dl's log means for the page.
type Line struct {
	Kind string // "page", "volume", "done", "error", "" (just chatter)
	Text string // for "page": "Vol. 1 Ch. 3 · page 12"; for "error": the message
}

var (
	pageLine   = regexp.MustCompile(`^\[INFO\] Downloading (?:\[[^\]]*\] )?(.*?) page (\d+)$`)
	volumeLine = regexp.MustCompile(`^\[INFO\] (.*) has finished download`)
	shortVol   = strings.NewReplacer("Volume. ", "Vol. ", "Chapter. ", "Ch. ")
)

// Parse reads one line of mangadex-dl's log (it logs to stderr, one line per
// event, with --progress-bar-layout none). It exits 0 even when it fails, so
// the [ERROR] lines are how a failure is noticed.
//
//	Parse(`[INFO] Downloading [group] Volume. 1 Chapter. 1 page 2`) // {page, "Vol. 1 Ch. 1 · page 2"}
//	Parse(`[ERROR] Manga 'X' with en language has no chapters`)      // {error, …}
func Parse(raw string) Line {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "[ERROR]"), strings.HasPrefix(s, "[CRITICAL]"):
		return Line{Kind: "error", Text: friendly(strings.TrimSpace(s[strings.Index(s, "]")+1:]))}
	case strings.HasPrefix(s, "Traceback (most recent call last)"):
		return Line{Kind: "error", Text: "mangadex-dl crashed (the details are in the log)"}
	case strings.HasPrefix(s, "[INFO] Download finished for manga"):
		return Line{Kind: "done"}
	}
	if m := pageLine.FindStringSubmatch(s); m != nil {
		return Line{Kind: "page", Text: shortVol.Replace(m[1]) + " · page " + m[2]}
	}
	if m := volumeLine.FindStringSubmatch(s); m != nil {
		return Line{Kind: "volume", Text: m[1]}
	}
	return Line{}
}

// friendly rewords the errors she's likely to meet.
func friendly(msg string) string {
	if strings.Contains(msg, "has no chapters") {
		return "MangaDex has no chapters of this in your language. Popular licensed series are often taken down there: try another language in Settings, or another series."
	}
	return msg
}
