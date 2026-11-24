// SPDX-License-Identifier: EUPL-1.2

package main

import (
	core "github.com/dappcore/go"

	"manga/downloader"
	"manga/library"
	"manga/mangadex"
	"manga/shelf"
)

// terminal is what the commands work with: where they print, MangaDex, and
// the shelf (her settings and progress). Tests give stand-ins for the last two.
type terminal struct {
	out       core.Writer
	dex       *mangadex.Client
	shelfPath string
}

// commands is Pinku without the window, through Core's command tree.
func commands(out core.Writer) *core.Core {
	return terminal{out: out, dex: mangadex.New(), shelfPath: shelf.DefaultPath()}.commands()
}

func (tm terminal) commands() *core.Core {
	c := core.New(core.WithOption("name", "pinku"), core.WithCli())
	c.Command("search", core.Command{Description: "search MangaDex: pinku search frieren", Action: func(o core.Options) core.Result {
		return tm.search(core.Join(" ", o.Args()...))
	}})
	c.Command("get", core.Command{Description: "download a series onto the shelf: pinku get <MangaDex link>", Action: func(o core.Options) core.Result {
		return tm.get(o.String("_arg"))
	}})
	c.Command("shelf", core.Command{Description: "what's on the shelf", Action: func(core.Options) core.Result {
		return tm.showShelf()
	}})
	c.Command("update", core.Command{Description: "look for new chapters (of one series, or all of them)", Action: func(o core.Options) core.Result {
		return tm.update(o.String("_arg"))
	}})
	return c
}

// isCommand: does args start with a command (rather than window flags)?
func isCommand(c *core.Core, args []string) bool {
	return len(args) > 0 && !core.IsFlag(args[0]) && c.Command(args[0]).OK
}

func (tm terminal) search(q string) core.Result {
	if core.Trim(q) == "" {
		return core.Fail(core.E("pinku.search", "search for what? pinku search frieren", nil))
	}
	found, err := tm.dex.Search(core.Background(), q, "en")
	if err != nil {
		return core.Fail(core.E("pinku.search", "searching", err))
	}
	if len(found) == 0 {
		core.Print(tm.out, "nothing on MangaDex called %q", q)
	}
	for _, m := range found {
		core.Print(tm.out, "%s  (%s%s)  %s", m.Title, m.Status, year(m.Year), m.URL)
	}
	return core.Ok(len(found))
}

func (tm terminal) get(link string) core.Result {
	if mangadex.ID(link) == "" {
		return core.Fail(core.E("pinku.get", "which one? pinku get <MangaDex link>", nil))
	}
	m, err := tm.dex.Get(core.Background(), link)
	if err != nil {
		return core.Fail(core.E("pinku.get", "asking MangaDex", err))
	}
	return tm.queueAndWait(func(d *downloader.Service, _ *shelf.Shelf) ([]downloader.Job, error) {
		j, err := d.Add(m)
		return []downloader.Job{j}, err
	})
}

func (tm terminal) update(folder string) core.Result {
	return tm.queueAndWait(func(d *downloader.Service, sh *shelf.Shelf) ([]downloader.Job, error) {
		if folder != "" {
			j, err := d.Update(folder)
			return []downloader.Job{j}, err
		}
		series, err := library.Library{Dir: sh.Settings().Library}.Scan()
		var jobs []downloader.Job
		for _, s := range series {
			if s.MangaDex != "" {
				if j, err := d.Update(s.Folder); err == nil {
					jobs = append(jobs, j)
				}
			}
		}
		return jobs, err
	})
}

// queueAndWait queues downloads and prints how they go until they're done.
func (tm terminal) queueAndWait(queue func(*downloader.Service, *shelf.Shelf) ([]downloader.Job, error)) core.Result {
	sh, c := open(tm.shelfPath)
	defer sh.Close()
	defer c.ServiceShutdown(core.Background())
	d := core.MustServiceFor[*downloader.Service](c, "downloader")
	jobs, err := queue(d, sh)
	if err != nil {
		return core.Fail(core.E("pinku", err.Error(), nil))
	}
	if len(jobs) == 0 {
		core.Print(tm.out, "%s", "nothing from MangaDex on the shelf to update")
		return core.Ok(0)
	}
	last := map[int]string{}
	for {
		for _, j := range d.Jobs() {
			line := j.State
			if j.Now != "" {
				line += ": " + j.Now
			}
			if j.Error != "" {
				line += ": " + j.Error
			}
			if last[j.ID] != line {
				last[j.ID] = line
				core.Print(tm.out, "%s · %s", j.Title, line)
			}
		}
		if !d.Busy() {
			break
		}
		core.Sleep(500 * core.Millisecond)
	}
	failed := 0
	for _, j := range d.Jobs() {
		if j.State == "failed" {
			failed++
		}
	}
	if failed > 0 {
		return core.Fail(core.E("pinku", core.Sprintf("%d of %d didn't download", failed, len(jobs)), nil))
	}
	return core.Ok(len(jobs))
}

func (tm terminal) showShelf() core.Result {
	r := shelf.Open(tm.shelfPath)
	if !r.OK {
		return r
	}
	sh := r.Value.(*shelf.Shelf)
	defer sh.Close()
	lib := library.Library{Dir: sh.Settings().Library}
	series, err := lib.Scan()
	if err != nil {
		return core.Fail(core.E("pinku.shelf", "reading "+lib.Dir, err))
	}
	core.Print(tm.out, "%s", lib.Dir)
	if len(series) == 0 {
		core.Print(tm.out, "%s", "  (empty: pinku search <title>, then pinku get <link>)")
	}
	for _, s := range series {
		read := 0
		p := sh.Progress(s.Folder)
		for _, b := range s.Books {
			if p[b.File].Done() {
				read++
			}
		}
		core.Print(tm.out, "  %-40s %3d books, %d read", s.Title, len(s.Books), read)
	}
	return core.Ok(len(series))
}

func year(y int) string {
	if y == 0 {
		return ""
	}
	return core.Sprintf(", %d", y)
}
