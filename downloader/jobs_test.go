// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/dappcore/go"
	process "github.com/dappcore/go-process/go"

	"manga/mangadex"
	"manga/shelf"
)

// startDownloader is a downloader service over a fresh bookcase, with tool
// as its mangadex-dl. changed hears each ShelfChanged.
func startDownloader(t *testing.T, tool string) (*Service, chan string, string) {
	t.Helper()
	lib := filepath.Join(t.TempDir(), "Manga")
	set := shelf.Settings{Library: lib, Language: "en", Format: "cbz-volume", Tool: tool}
	c := core.New(
		core.WithService(process.Register),
		core.WithService(Register(Options{DataDir: t.TempDir(), Settings: func() shelf.Settings { return set }})),
		core.WithServiceLock(),
	)
	changed := make(chan string, 8)
	core.On(c, func(m ShelfChanged) core.Result { changed <- m.Folder; return core.Ok(nil) })
	if r := c.ServiceStartup(core.Background(), nil); !r.OK {
		t.Fatal(r.Error())
	}
	t.Cleanup(func() { c.ServiceShutdown(core.Background()) })
	return core.MustServiceFor[*Service](c, "downloader"), changed, lib
}

// waitFor polls until cond holds (or fails the test after 15s).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func job(d *Service, id int) Job {
	for _, j := range d.Jobs() {
		if j.ID == id {
			return j
		}
	}
	return Job{}
}

// Stop really stops: a running download is cancelled, the tool is killed
// (no volume appears later), Busy goes false, and the next job still runs.
func TestCancelARunningDownload(t *testing.T) {
	d, _, lib := startDownloader(t, fakeTool(t))
	slow, err := d.Add(mangadex.Manga{ID: "cbe84ea7-2992-4db0-b4ef-d042124f48c8", Title: "SLOW one"})
	if err != nil {
		t.Fatal(err)
	}
	next, _ := d.Add(mangadex.Manga{ID: "9906dfa8-52c9-40ac-8eef-1099a85c8611", Title: "After it"})
	waitFor(t, "the slow job to start", func() bool {
		return job(d, slow.ID).State == "running" && strings.Contains(job(d, slow.ID).Now, "page 1")
	})
	if !d.Busy() {
		t.Fatal("Busy() is false with a download running")
	}
	if !d.Cancel(slow.ID) {
		t.Fatal("Cancel said no")
	}
	waitFor(t, "the queue to move on", func() bool { return job(d, next.ID).State == "done" })
	if j := job(d, slow.ID); j.State != "cancelled" || j.Finished.IsZero() {
		t.Errorf("cancelled job: %+v", j)
	}
	if core.Stat(filepath.Join(lib, slow.Folder, "Vol. 1.cbz")).OK {
		t.Error("the cancelled download still wrote its volume")
	}
	waitFor(t, "Busy to clear", func() bool { return !d.Busy() })
	if d.Cancel(next.ID) {
		t.Error("cancelled a finished job")
	}
	if d.Cancel(424242) {
		t.Error("cancelled a job that doesn't exist")
	}
}

// A queued job can be taken out of the queue before it starts.
func TestCancelAQueuedDownload(t *testing.T) {
	d, _, _ := startDownloader(t, fakeTool(t))
	first, _ := d.Add(mangadex.Manga{ID: "cbe84ea7-2992-4db0-b4ef-d042124f48c8", Title: "SLOW first"})
	queued, _ := d.Add(mangadex.Manga{ID: "9906dfa8-52c9-40ac-8eef-1099a85c8611", Title: "Second"})
	if !d.Cancel(queued.ID) {
		t.Fatal("couldn't take it out of the queue")
	}
	if j := job(d, queued.ID); j.State != "cancelled" {
		t.Errorf("queued job after Cancel: %+v", j)
	}
	d.Cancel(first.ID)
}

// The log keeps what mangadex-dl said, for the Log button.
func TestLogKeepsTheToolsOutput(t *testing.T) {
	d, changed, _ := startDownloader(t, fakeTool(t))
	j, _ := d.Add(mangadex.Manga{ID: "cbe84ea7-2992-4db0-b4ef-d042124f48c8", Title: "Logged"})
	select {
	case <-changed:
	case <-time.After(15 * time.Second):
		t.Fatal("download didn't finish")
	}
	log := strings.Join(d.Log(j.ID), "\n")
	if !strings.Contains(log, "Checking url") || !strings.Contains(log, "Download finished") {
		t.Errorf("log: %q", log)
	}
	if d.Log(424242) != nil {
		t.Error("a log for a job that doesn't exist")
	}
}

// No mangadex-dl and none to fetch: the job fails saying so, rather than hanging.
func TestNoToolFailsClearly(t *testing.T) {
	if CanFetch() {
		t.Skip("here it would download the real one")
	}
	t.Setenv("PATH", t.TempDir()) // nothing on the PATH either
	d, _, _ := startDownloader(t, "")
	j, _ := d.Add(mangadex.Manga{ID: "cbe84ea7-2992-4db0-b4ef-d042124f48c8", Title: "Toolless"})
	waitFor(t, "the job to fail", func() bool { return job(d, j.ID).State == "failed" })
	if e := job(d, j.ID).Error; !strings.Contains(e, "mangadex") {
		t.Errorf("error doesn't say what's missing: %q", e)
	}
}
