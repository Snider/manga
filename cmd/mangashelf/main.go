// SPDX-License-Identifier: EUPL-1.2

// mangashelf is Manga Shelf: a bookcase for manga, a MangaDex search, and
// mangadex-dl doing the downloading, in one window. No terminal needed.
//
//	mangashelf                       # the window
//	mangashelf search frieren        # the same things, from a terminal
//	mangashelf get https://mangadex.org/title/<id>
//	mangashelf shelf
//	mangashelf update                # look for new chapters of everything
//
// The page talks to the program over the window's own asset server (/api/...),
// inside the process: no port is opened. The network is only used to talk to
// MangaDex (search, covers, and mangadex-dl's downloads) and, once, to fetch
// mangadex-dl itself from its GitHub release if it isn't next to the program.
package main

import (
	"embed"
	"io/fs"
	"log"
	"os"

	core "github.com/dappcore/go"
	process "github.com/dappcore/go-process/go"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"manga/downloader"
	"manga/mangadex"
	"manga/shelf"
)

//go:embed frontend
var frontend embed.FS

func main() {
	if len(os.Args) > 1 && !core.IsFlag(os.Args[1]) {
		attachConsole() // before anything prints: the command's output needs somewhere to go
	}
	if c := commands(os.Stdout); isCommand(c, os.Args[1:]) {
		if r := c.Cli().Run(os.Args[1:]...); !r.OK {
			log.Fatal(r.Error())
		}
		return
	}

	sh, c := open()
	defer sh.Close()
	page, _ := fs.Sub(frontend, "frontend")
	a := &app{
		shelf:  sh,
		dex:    mangadex.New(),
		down:   core.MustServiceFor[*downloader.Service](c, "downloader"),
		assets: application.BundledAssetFileServer(page),
		open:   func(path string) error { return application.Get().Browser.OpenFile(path) },
		browse: func(url string) error { return application.Get().Browser.OpenURL(url) },
		pickBooks: func() ([]string, error) {
			return application.Get().Dialog.OpenFile().CanChooseFiles(true).SetTitle("Add manga to the shelf").
				AddFilter("Manga (.cbz, .pdf, .epub, .cb7)", "*.cbz;*.pdf;*.epub;*.cb7").PromptForMultipleSelection()
		},
		pick: func(title string) (string, error) {
			return application.Get().Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true).SetTitle(title).PromptForSingleSelection()
		},
	}
	win := application.New(application.Options{
		Name:        "Manga Shelf",
		Description: "A bookcase for manga, with MangaDex downloads",
		Assets:      application.AssetOptions{Handler: a.handler()},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	w := win.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Manga Shelf",
		Width:            1180,
		Height:           800,
		MinWidth:         640,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(24, 20, 18),
		URL:              "/",
		EnableFileDrop:   true, // drop .cbz files or folders of them onto the window
	})
	w.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		a.Import(e.Context().DroppedFiles())
	})
	err := win.Run()
	c.ServiceShutdown(core.Background()) // stops a download mid-way: "update" carries on from there
	if err != nil {
		log.Fatal(err)
	}
}

// open is the shelf (settings, progress) plus the Core with the download
// queue in it. Both the window and the terminal commands start here.
func open() (*shelf.Shelf, *core.Core) {
	r := shelf.Open(shelf.DefaultPath())
	if !r.OK {
		log.Fatalf("can't open %s: %s", shelf.DefaultPath(), r.Error())
	}
	sh := r.Value.(*shelf.Shelf)
	c := core.New(
		core.WithOption("name", "mangashelf"),
		core.WithService(process.Register),
		core.WithService(downloader.Register(downloader.Options{DataDir: shelf.Dir(), Settings: sh.Settings})),
		core.WithServiceLock(),
	)
	if r := c.ServiceStartup(c.Context(), nil); !r.OK {
		log.Fatal(r.Error())
	}
	return sh, c
}
