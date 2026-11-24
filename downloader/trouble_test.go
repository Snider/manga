// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/dappcore/go"
	process "github.com/dappcore/go-process/go"

	"manga/mangadex"
	"manga/shelf"
)

const uso = "cbe84ea7-2992-4db0-b4ef-d042124f48c8"

// Closing Pinku mid-download stops mangadex-dl, rather than waiting it out.
func TestShutdownStopsARunningDownload(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "Manga")
	set := shelf.Settings{Library: lib, Language: "en", Format: "cbz-volume", Tool: fakeTool(t)}
	c := core.New(
		core.WithService(process.Register),
		core.WithService(Register(Options{DataDir: t.TempDir(), Settings: func() shelf.Settings { return set }})),
		core.WithServiceLock(),
	)
	if r := c.ServiceStartup(core.Background(), nil); !r.OK {
		t.Fatal(r.Error())
	}
	d := core.MustServiceFor[*Service](c, "downloader")
	j, _ := d.Add(mangadex.Manga{ID: uso, Title: "SLOW closing"})
	waitFor(t, "it to start", func() bool { return job(d, j.ID).State == "running" && job(d, j.ID).Now != "" })
	start := time.Now()
	c.ServiceShutdown(core.Background())
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("shutdown waited %s for the download", took)
	}
}

// A tool that won't start, and a bookcase folder that can't be made, fail
// the job with a message she can read.
func TestRunFailures(t *testing.T) {
	notAProgram := filepath.Join(t.TempDir(), "mangadex-dl.txt")
	os.WriteFile(notAProgram, []byte("not a program"), 0o644)
	d, _, _ := startDownloader(t, notAProgram)
	j, _ := d.Add(mangadex.Manga{ID: uso, Title: "Unstartable"})
	waitFor(t, "it to fail", func() bool { return job(d, j.ID).State == "failed" })
	if e := job(d, j.ID).Error; !strings.Contains(e, "couldn't start") {
		t.Errorf("error = %q", e)
	}

	file := filepath.Join(t.TempDir(), "Manga")
	os.WriteFile(file, []byte("a file where the bookcase should be"), 0o644)
	set := shelf.Settings{Library: file, Language: "en", Format: "cbz-volume", Tool: fakeTool(t)}
	c := core.New(
		core.WithService(process.Register),
		core.WithService(Register(Options{DataDir: t.TempDir(), Settings: func() shelf.Settings { return set }})),
		core.WithServiceLock(),
	)
	c.ServiceStartup(core.Background(), nil)
	t.Cleanup(func() { c.ServiceShutdown(core.Background()) })
	d2 := core.MustServiceFor[*Service](c, "downloader")
	if _, err := d2.Add(mangadex.Manga{ID: uso, Title: "Nowhere"}); err == nil {
		t.Error("added a series with no bookcase to put it in")
	}
}

// The log keeps the last 300 lines: a long download can't grow it forever.
func TestLogIsCapped(t *testing.T) {
	d, changed, _ := startDownloader(t, fakeTool(t))
	j, _ := d.Add(mangadex.Manga{ID: uso, Title: "NOISY one"})
	select {
	case <-changed:
	case <-time.After(15 * time.Second):
		t.Fatal("download didn't finish")
	}
	log := d.Log(j.ID)
	if len(log) != 300 || !strings.Contains(log[len(log)-1], "Download finished") {
		t.Errorf("log has %d lines, ends %q", len(log), log[len(log)-1])
	}
}
