// SPDX-License-Identifier: EUPL-1.2

// Package downloader runs mangadex-dl for the shelf, one series at a time,
// through go-process, and turns its log into something the page can show.
//
//	c := core.New(
//		core.WithService(process.Register),
//		core.WithService(downloader.Register(downloader.Options{DataDir: dir, Settings: shelf.Settings})),
//	)
//	d := core.MustServiceFor[*downloader.Service](c, "downloader")
//	d.Add(manga)            // a new series: folder + manga.json, then download it
//	d.Update("Uso-douri…")  // fetch whatever's new since last time
//	d.Jobs()                // for the Downloads page
package downloader

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	core "github.com/dappcore/go"
	process "github.com/dappcore/go-process/go"

	"manga/library"
	"manga/mangadex"
	"manga/shelf"
)

// Options: where Manga Shelf keeps its things, and its settings (read fresh for each job).
type Options struct {
	DataDir  string
	Settings func() shelf.Settings
}

// Job is one series being downloaded.
type Job struct {
	ID       int       `json:"id"`
	Folder   string    `json:"folder"`
	Title    string    `json:"title"`
	State    string    `json:"state"` // queued, running, done, failed, cancelled
	Now      string    `json:"now"`   // "Vol. 2 Ch. 9 · page 14"
	Volumes  int       `json:"volumes"`
	Pages    int       `json:"pages"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Log      []string  `json:"-"`

	url, format, language string
	proc                  *process.Process
}

// ShelfChanged goes out on the bus when a download finishes: the bookcase has new books.
type ShelfChanged struct{ Folder string }

// Service is the download queue.
type Service struct {
	*core.ServiceRuntime[Options]
	mu      sync.Mutex
	jobs    []*Job
	next    int
	wake    chan struct{}
	pending map[string][]string // output that arrived before we knew whose it was
	procs   *process.Service
}

// Register builds the service.
func Register(opts Options) func(*core.Core) core.Result {
	return func(c *core.Core) core.Result {
		s := &Service{ServiceRuntime: core.NewServiceRuntime(c, opts), wake: make(chan struct{}, 1), pending: map[string][]string{}}
		core.On(c, func(m process.ActionProcessOutput) core.Result {
			s.output(m.ID, m.Line)
			return core.Ok(nil)
		})
		return core.Ok(s)
	}
}

// OnStartup starts the worker.
func (s *Service) OnStartup(ctx core.Context) core.Result {
	s.procs = core.MustServiceFor[*process.Service](s.Core(), "process")
	s.Core().Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			}
			for j := s.take(); j != nil; j = s.take() {
				s.run(ctx, j)
			}
		}
	})
	return core.Ok(nil)
}

// OnShutdown stops a running download (mangadex-dl picks up where it left off next time).
func (s *Service) OnShutdown(core.Context) core.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.State == "running" && j.proc != nil {
			j.proc.Kill()
		}
	}
	return core.Ok(nil)
}

// Add puts a new series on the shelf and queues its download. If it's
// already there, it's an Update instead.
func (s *Service) Add(m mangadex.Manga) (Job, error) {
	set := s.Options().Settings()
	lib := library.Library{Dir: set.Library}
	base := library.FolderName(m.Title)
	folder := base
	for n := 2; ; n++ { // a folder that's taken (another series, or one she made) means "Title (2)"
		if info, err := lib.Info(folder); err == nil && info.MangaDex == m.ID {
			return s.Update(folder)
		}
		dir, err := lib.Path(folder)
		if err != nil {
			return Job{}, err
		}
		if _, err := os.Stat(dir); err != nil {
			break // free
		}
		folder = base + " (" + strconv.Itoa(n) + ")"
	}
	info := library.Info{MangaDex: m.ID, Title: m.Title, URL: mangadex.URL(m.ID), Language: set.Language, Format: set.Format, Added: time.Now()}
	if err := lib.WriteInfo(folder, info); err != nil {
		return Job{}, fmt.Errorf("couldn't make the folder for %s: %w", m.Title, err)
	}
	return s.queue(folder, info)
}

// Update queues a series' download again: mangadex-dl skips what's already there.
func (s *Service) Update(folder string) (Job, error) {
	info, err := library.Library{Dir: s.Options().Settings().Library}.Info(folder)
	if err != nil {
		return Job{}, fmt.Errorf("%s wasn't downloaded from MangaDex, so there's nothing to check", folder)
	}
	return s.queue(folder, info)
}

func (s *Service) queue(folder string, info library.Info) (Job, error) {
	// manga.json sits in her folder where anything could edit it, so nothing
	// from it reaches mangadex-dl's command line unchecked: the link is
	// rebuilt from the id, the format and language must be ones we offer.
	id := mangadex.ID(info.MangaDex)
	if id == "" || id != info.MangaDex {
		return Job{}, fmt.Errorf("%s/manga.json doesn't name a MangaDex manga", folder)
	}
	if shelf.Formats[info.Format] == "" {
		info.Format = s.Options().Settings().Format
	}
	if !shelf.ValidLanguage(info.Language) {
		info.Language = s.Options().Settings().Language
	}
	info.URL = mangadex.URL(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.Folder == folder && (j.State == "queued" || j.State == "running") {
			return j.view(), nil // already on its way
		}
	}
	s.next++
	j := &Job{ID: s.next, Folder: folder, Title: info.Title, State: "queued", url: info.URL, format: info.Format, language: info.Language}
	s.jobs = append(s.jobs, j)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return j.view(), nil
}

// Jobs is the queue, newest first (finished ones stay until the program closes).
func (s *Service) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for i := len(s.jobs) - 1; i >= 0; i-- {
		out = append(out, s.jobs[i].view())
	}
	return out
}

// Busy: is anything queued or running?
func (s *Service) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.State == "queued" || j.State == "running" {
			return true
		}
	}
	return false
}

// Log is a job's mangadex-dl output (the last 300 lines).
func (s *Service) Log(id int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID == id {
			return append([]string(nil), j.Log...)
		}
	}
	return nil
}

// Cancel stops a job (running) or takes it out of the queue.
func (s *Service) Cancel(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID != id {
			continue
		}
		switch j.State {
		case "queued":
			j.State, j.Finished = "cancelled", time.Now()
			return true
		case "running":
			j.State = "cancelled"
			if j.proc != nil {
				j.proc.Kill()
			}
			return true
		}
	}
	return false
}

func (s *Service) take() *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.State == "queued" {
			j.State, j.Started = "running", time.Now()
			return j
		}
	}
	return nil
}

// Args is the mangadex-dl command line for a job. The link goes last, after
// "--", so nothing in it can be read as an option.
func Args(url, dir, format, language string) []string {
	return []string{"--path", dir, "--save-as", format, "--language", language, "--cover", "512px", "--progress-bar-layout", "none", "--", url}
}

func (s *Service) run(ctx core.Context, j *Job) {
	set := s.Options().Settings()
	tool := FindTool(set.Tool, s.Options().DataDir)
	if tool == "" && CanFetch() {
		var err error
		tool, err = FetchTool(ctx, s.Options().DataDir, func(msg string) { s.set(j, func() { j.Now = msg }) })
		if err != nil {
			s.finish(j, err.Error())
			return
		}
	}
	if tool == "" {
		s.finish(j, "mangadex-dl isn't installed: pip install mangadex-downloader, or set where it is in Settings")
		return
	}
	dir, err := library.Library{Dir: set.Library}.Path(j.Folder)
	if err != nil {
		s.finish(j, err.Error())
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.finish(j, err.Error())
		return
	}
	r := s.procs.StartWithOptions(ctx, process.RunOptions{
		Command: tool,
		Args:    Args(j.url, dir, j.format, j.language),
		Dir:     filepath.Dir(tool),
		Env:     []string{"PYTHONIOENCODING=utf-8", "PYTHONUTF8=1"}, // titles in any script, piped on Windows
	})
	if !r.OK {
		s.finish(j, "couldn't start mangadex-dl: "+r.Error())
		return
	}
	proc := r.Value.(*process.Process)
	proc.CloseStdin() // it never needs to ask anything: we pass the link
	s.mu.Lock()
	j.proc = proc
	early := s.pending[proc.ID]
	delete(s.pending, proc.ID)
	s.mu.Unlock()
	for _, line := range early {
		s.output(proc.ID, line)
	}
	<-proc.Done()
	info := proc.Info()
	s.mu.Lock()
	cancelled := j.State == "cancelled"
	failed := j.Error
	s.mu.Unlock()
	switch {
	case cancelled:
		s.finish(j, "")
	case failed != "":
		s.finish(j, failed)
	case info.ExitCode != 0:
		s.finish(j, fmt.Sprintf("mangadex-dl stopped (exit %d): see the log", info.ExitCode))
	default:
		s.finish(j, "")
	}
	s.procs.Remove(proc.ID)
}

// output is one line from some process: ours, or someone else's.
func (s *Service) output(procID, raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var j *Job
	for _, candidate := range s.jobs {
		if candidate.proc != nil && candidate.proc.ID == procID {
			j = candidate
		}
	}
	if j == nil {
		if len(s.pending[procID]) < 200 {
			s.pending[procID] = append(s.pending[procID], raw)
		}
		return
	}
	j.Log = append(j.Log, raw)
	if len(j.Log) > 300 {
		j.Log = j.Log[len(j.Log)-300:]
	}
	switch l := Parse(raw); l.Kind {
	case "page":
		j.Now, j.Pages = l.Text, j.Pages+1
	case "volume":
		j.Volumes++
	case "error":
		if j.Error == "" {
			j.Error = l.Text
		}
	}
}

func (s *Service) set(j *Job, f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

func (s *Service) finish(j *Job, failure string) {
	s.mu.Lock()
	switch {
	case j.State == "cancelled":
	case failure != "":
		j.State, j.Error = "failed", failure
	default:
		j.State, j.Error = "done", ""
	}
	j.Now, j.Finished, j.proc = "", time.Now(), nil
	s.mu.Unlock()
	s.Core().ACTION(ShelfChanged{Folder: j.Folder})
}

func (j *Job) view() Job {
	v := *j
	v.Log, v.proc = nil, nil
	return v
}
