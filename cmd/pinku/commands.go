// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	core "github.com/dappcore/go"

	"manga/downloader"
	"manga/library"
	"manga/mangadex"
	"manga/shelf"
)

// commands is Pinku without the window, through Core's command tree.
func commands(out io.Writer) *core.Core {
	c := core.New(core.WithOption("name", "pinku"), core.WithCli())
	c.Command("search", core.Command{Description: "search MangaDex: pinku search frieren", Action: func(o core.Options) core.Result {
		return search(out, strings.Join(o.Args(), " "))
	}})
	c.Command("get", core.Command{Description: "download a series onto the shelf: pinku get <MangaDex link>", Action: func(o core.Options) core.Result {
		return get(out, o.String("_arg"))
	}})
	c.Command("shelf", core.Command{Description: "what's on the shelf", Action: func(core.Options) core.Result {
		return showShelf(out)
	}})
	c.Command("update", core.Command{Description: "look for new chapters (of one series, or all of them)", Action: func(o core.Options) core.Result {
		return update(out, o.String("_arg"))
	}})
	return c
}

// isCommand: does args start with a command (rather than window flags)?
func isCommand(c *core.Core, args []string) bool {
	return len(args) > 0 && !core.IsFlag(args[0]) && c.Command(args[0]).OK
}

func search(out io.Writer, q string) core.Result {
	if strings.TrimSpace(q) == "" {
		return core.Fail(core.E("pinku.search", "search for what? pinku search frieren", nil))
	}
	found, err := mangadex.New().Search(context.Background(), q, "en")
	if err != nil {
		return core.Fail(core.E("pinku.search", "searching", err))
	}
	if len(found) == 0 {
		fmt.Fprintf(out, "nothing on MangaDex called %q\n", q)
	}
	for _, m := range found {
		fmt.Fprintf(out, "%s  (%s%s)  %s\n", m.Title, m.Status, year(m.Year), m.URL)
	}
	return core.Ok(len(found))
}

func get(out io.Writer, link string) core.Result {
	if mangadex.ID(link) == "" {
		return core.Fail(core.E("pinku.get", "which one? pinku get <MangaDex link>", nil))
	}
	m, err := mangadex.New().Get(context.Background(), link)
	if err != nil {
		return core.Fail(core.E("pinku.get", "asking MangaDex", err))
	}
	return queueAndWait(out, func(d *downloader.Service, _ *shelf.Shelf) ([]downloader.Job, error) {
		j, err := d.Add(m)
		return []downloader.Job{j}, err
	})
}

func update(out io.Writer, folder string) core.Result {
	return queueAndWait(out, func(d *downloader.Service, sh *shelf.Shelf) ([]downloader.Job, error) {
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
func queueAndWait(out io.Writer, queue func(*downloader.Service, *shelf.Shelf) ([]downloader.Job, error)) core.Result {
	sh, c := open()
	defer sh.Close()
	defer c.ServiceShutdown(core.Background())
	d := core.MustServiceFor[*downloader.Service](c, "downloader")
	jobs, err := queue(d, sh)
	if err != nil {
		return core.Fail(core.E("pinku", err.Error(), nil))
	}
	if len(jobs) == 0 {
		fmt.Fprintln(out, "nothing from MangaDex on the shelf to update")
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
				fmt.Fprintf(out, "%s · %s\n", j.Title, line)
			}
		}
		if !d.Busy() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	failed := 0
	for _, j := range d.Jobs() {
		if j.State == "failed" {
			failed++
		}
	}
	if failed > 0 {
		return core.Fail(core.E("pinku", fmt.Sprintf("%d of %d didn't download", failed, len(jobs)), nil))
	}
	return core.Ok(len(jobs))
}

func showShelf(out io.Writer) core.Result {
	r := shelf.Open(shelf.DefaultPath())
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
	fmt.Fprintln(out, lib.Dir)
	if len(series) == 0 {
		fmt.Fprintln(out, "  (empty: pinku search <title>, then pinku get <link>)")
	}
	for _, s := range series {
		read := 0
		p := sh.Progress(s.Folder)
		for _, b := range s.Books {
			if p[b.File].Done() {
				read++
			}
		}
		fmt.Fprintf(out, "  %-40s %3d books, %d read\n", s.Title, len(s.Books), read)
	}
	return core.Ok(len(series))
}

func year(y int) string {
	if y == 0 {
		return ""
	}
	return fmt.Sprintf(", %d", y)
}
