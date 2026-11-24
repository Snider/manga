// SPDX-License-Identifier: EUPL-1.2

package main

import (
	core "github.com/dappcore/go"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"manga/shelf"
)

// This file is the only code the tests can't run: main and the window need a
// desktop and a person (drag and drop, the file dialogs), and fatal exits.
// Each step main takes (runTerminal, openShelf, newApp) is tested on its own;
// codecov.yml leaves this file out of the figure for that reason.

func main() {
	if handled, r := runTerminal(core.Args()[1:], core.Stdout()); handled {
		if !r.OK {
			fatal(r.Error())
		}
		return
	}
	sh, c, err := openShelf(shelf.DefaultPath())
	if err != nil {
		fatal(err)
	}
	defer sh.Close()
	err = runWindow(newApp(sh, c))       // window.go: the one part only a person can test
	c.ServiceShutdown(core.Background()) // stops a download mid-way: "update" carries on from there
	if err != nil {
		fatal(err)
	}
}

// fatal reports what stopped Pinku from starting, and exits.
func fatal(v any) {
	core.Error(core.Sprintf("%v", v))
	core.Exit(1)
}

// runWindow opens the window and blocks until it's closed. Everything it
// serves is newApp's.
func runWindow(a *app) error {
	a.assets = application.BundledAssetFileServer(core.Sub(frontend, "frontend").Value.(core.FS))
	a.open = func(path string) error { return application.Get().Browser.OpenFile(path) }
	a.browse = func(url string) error { return application.Get().Browser.OpenURL(url) }
	a.pickBooks = func() ([]string, error) {
		return application.Get().Dialog.OpenFile().CanChooseFiles(true).SetTitle("Add manga to the shelf").
			AddFilter("Manga (.cbz, .pdf, .epub, .cb7)", "*.cbz;*.pdf;*.epub;*.cb7").PromptForMultipleSelection()
	}
	a.pick = func(title string) (string, error) {
		return application.Get().Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true).SetTitle(title).PromptForSingleSelection()
	}
	win := application.New(application.Options{
		Name:        "Pinku",
		Description: "A bookcase for manga, with MangaDex downloads",
		Assets:      application.AssetOptions{Handler: a.handler()},
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
	})
	w := win.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Pinku",
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
	return win.Run()
}
