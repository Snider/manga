// SPDX-License-Identifier: EUPL-1.2

// pinku is Pinku: a bookcase for manga, a MangaDex search, and
// mangadex-dl doing the downloading, in one window. No terminal needed.
//
//	pinku                       # the window
//	pinku search frieren        # the same things, from a terminal
//	pinku get https://mangadex.org/title/<id>
//	pinku shelf
//	pinku update                # look for new chapters of everything
//
// The page talks to the program over the window's own asset server (/api/...),
// inside the process: no port is opened. The network is only used to talk to
// MangaDex (search, covers, and mangadex-dl's downloads) and, once, to fetch
// mangadex-dl itself from its GitHub release if it isn't next to the program.
package main

import (
	"embed"

	core "github.com/dappcore/go"
	process "github.com/dappcore/go-process/go"

	"manga/downloader"
	"manga/mangadex"
	"manga/shelf"
)

//go:embed frontend
var frontend embed.FS

// runTerminal runs a command (pinku search frieren…) if args start with one,
// borrowing the terminal's console first so the output shows. handled is
// false for the window (no args, or window flags).
func runTerminal(args []string, out core.Writer) (handled bool, r core.Result) {
	if len(args) > 0 && !core.IsFlag(args[0]) {
		core.AttachParentConsole() // before anything prints
		out = core.Stdout()        // which may have just become the console
	}
	c := commands(out)
	if !isCommand(c, args) {
		return false, core.Ok(nil)
	}
	return true, c.Cli().Run(args...)
}

// newApp is everything the window serves except the window's own calls
// (opening Explorer, the browser, its file dialogs: runWindow adds those).
func newApp(sh *shelf.Shelf, c *core.Core) *app {
	return &app{
		shelf:  sh,
		dex:    mangadex.New(),
		down:   core.MustServiceFor[*downloader.Service](c, "downloader"),
		assets: core.HTTPFileServer(core.HTTPFS(core.Sub(frontend, "frontend").Value.(core.FS))),
		words:  loadWords(),
	}
}

// openShelf is the shelf at path (settings, progress; shelf.DefaultPath for
// real) plus the Core with the download queue in it. Both the window and the
// terminal commands start here.
func openShelf(path string) (*shelf.Shelf, *core.Core, error) {
	r := shelf.Open(path)
	if !r.OK {
		return nil, nil, core.E("pinku", "can't open "+path, r.Err())
	}
	sh := r.Value.(*shelf.Shelf)
	c := core.New(
		core.WithOption("name", "pinku"),
		core.WithService(process.Register),
		core.WithService(downloader.Register(downloader.Options{DataDir: core.PathDir(path), Settings: sh.Settings})),
		core.WithServiceLock(),
	)
	if r := c.ServiceStartup(c.Context(), nil); !r.OK {
		sh.Close()
		return nil, nil, core.E("pinku", "starting the download queue", r.Err())
	}
	return sh, c, nil
}

// loadWords: the page's text in her language. If it can't load, the page
// keeps the English it's written in.
func loadWords() *words {
	w, err := newWords()
	if err != nil {
		core.Warn("translations didn't load: the window stays in English", "err", err)
		return nil
	}
	return w
}
