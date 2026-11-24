// SPDX-License-Identifier: EUPL-1.2

package main

import (
	core "github.com/dappcore/go"

	"manga/library"
)

// importer runs one import at a time (files dropped on the window, or picked
// with "Add files…") and keeps the last result for the page to show.
type importer struct {
	mu   core.Mutex
	busy bool
	n    int // counts imports, so the page can tell a new result from an old one
	last library.Imported
}

// importState is what /api/imports answers.
type importState struct {
	Busy   bool             `json:"busy"`
	N      int              `json:"n"`
	Result library.Imported `json:"result"`
}

// start imports paths in the background; false if one's already running.
func (im *importer) start(lib library.Library, paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	im.mu.Lock()
	if im.busy {
		im.mu.Unlock()
		return false
	}
	im.busy = true
	im.mu.Unlock()
	go func() {
		got := lib.Import(paths)
		im.mu.Lock()
		im.busy, im.last = false, got
		im.n++
		im.mu.Unlock()
	}()
	return true
}

func (im *importer) state() importState {
	im.mu.Lock()
	defer im.mu.Unlock()
	return importState{Busy: im.busy, N: im.n, Result: im.last}
}
