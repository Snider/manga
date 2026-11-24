// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	core "github.com/dappcore/go"
	process "github.com/dappcore/go-process/go"

	"manga/library"
	"manga/mangadex"
	"manga/shelf"
)

// Real lines from a mangadex-dl 3.1.4 run (Uso-douri no Kottouya, 2 pages).
func TestParseRealLog(t *testing.T) {
	for raw, want := range map[string]Line{
		`[INFO] Checking url = https://mangadex.org/title/cbe84ea7-2992-4db0-b4ef-d042124f48c8`: {},
		`[INFO] Downloading [jlnsgr no sumika] Volume. 1 Chapter. 1 page 2`:                     {Kind: "page", Text: "Vol. 1 Ch. 1 · page 2"},
		`[INFO] Downloading Chapter. 7 page 10`:                                                 {Kind: "page", Text: "Ch. 7 · page 10"},
		`[INFO] Vol. 1 has finished download, converting chapters to .cbz file...`:              {Kind: "volume", Text: "Vol. 1"},
		`[INFO] Download finished for manga "Uso-douri no Kottouya"`:                            {Kind: "done"},
		`[INFO] Downloading cover manga Uso-douri no Kottouya`:                                  {},
		`[ERROR] Something else broke`:                                                          {Kind: "error", Text: "Something else broke"},
	} {
		if got := Parse(raw); got != want {
			t.Errorf("Parse(%q) = %+v, want %+v", raw, got, want)
		}
	}
	if got := Parse(`[ERROR] Manga 'Sousou no Frieren Anthology' with en language has no chapters`); got.Kind != "error" || !strings.Contains(got.Text, "no chapters of this in your language") {
		t.Errorf("no-chapters: %+v", got)
	}
}

func zipOf(t *testing.T, files map[string]string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := z.Create(name)
		w.Write([]byte(body))
	}
	z.Close()
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestInstallTool(t *testing.T) {
	dir := t.TempDir()
	body, sha := zipOf(t, map[string]string{"mangadex-dl/mangadex-dl.exe": "MZ", "mangadex-dl/_internal/x.pyd": "x"})
	exe, err := installTool(body, sha, dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "MZ" || exe != filepath.Join(dir, "mangadex-dl", "mangadex-dl.exe") {
		t.Errorf("installed %s", exe)
	}
	if got := FindTool("", filepath.Dir(dir)); runtime.GOOS == "windows" && got != exe {
		t.Errorf("FindTool found %q", got)
	}
	// And again over the top (an upgrade): still fine.
	if _, err := installTool(body, sha, dir); err != nil {
		t.Errorf("reinstall: %v", err)
	}
}

func TestInstallToolRefusesBadZips(t *testing.T) {
	dir := t.TempDir()
	body, _ := zipOf(t, map[string]string{"mangadex-dl/mangadex-dl.exe": "MZ"})
	if _, err := installTool(body, strings.Repeat("0", 64), dir); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("wrong checksum: %v", err)
	}
	for _, files := range []map[string]string{
		{"mangadex-dl/../../evil.exe": "x", "mangadex-dl/mangadex-dl.exe": "MZ"},
		{"elsewhere/mangadex-dl.exe": "MZ"},
		{"mangadex-dl/readme.txt": "no exe"},
	} {
		body, sha := zipOf(t, files)
		if _, err := installTool(body, sha, dir); err == nil {
			t.Errorf("installTool(%v) should fail", files)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.exe")); err == nil {
		t.Error("a zip wrote outside its folder")
	}
}

func TestArgs(t *testing.T) {
	got := strings.Join(Args("https://mangadex.org/title/x", "/lib/X", "cbz-volume", "en"), " ")
	if got != "https://mangadex.org/title/x --path /lib/X --save-as cbz-volume --language en --cover 512px --progress-bar-layout none" {
		t.Errorf("args: %s", got)
	}
}

// fakeTool is a stand-in mangadex-dl: logs like the real one (to stderr) and
// writes a volume into --path. FAIL in the title makes it log an error instead.
const fakeTool = `#!/bin/sh
dir=""
while [ $# -gt 0 ]; do [ "$1" = "--path" ] && dir="$2"; shift; done
echo "[INFO] Checking url" >&2
case "$dir" in *FAIL*) echo "[ERROR] Manga 'x' with en language has no chapters" >&2; exit 0;; esac
echo "[INFO] Downloading [grp] Volume. 1 Chapter. 1 page 1" >&2
echo "[INFO] Downloading [grp] Volume. 1 Chapter. 1 page 2" >&2
echo "[INFO] Vol. 1 has finished download, converting chapters to .cbz file..." >&2
printf 'cbz' > "$dir/Vol. 1.cbz"
echo '[INFO] Download finished for manga "x"' >&2
`

func TestQueueRunsTheTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	tool := filepath.Join(t.TempDir(), "mangadex-dl")
	os.WriteFile(tool, []byte(fakeTool), 0o755)
	lib := filepath.Join(t.TempDir(), "Manga")
	set := shelf.Settings{Library: lib, Language: "en", Format: "cbz-volume", Tool: tool}
	c := core.New(
		core.WithService(process.Register),
		core.WithService(Register(Options{DataDir: t.TempDir(), Settings: func() shelf.Settings { return set }})),
		core.WithServiceLock(),
	)
	changed := make(chan string, 4)
	core.On(c, func(m ShelfChanged) core.Result { changed <- m.Folder; return core.Ok(nil) })
	if r := c.ServiceStartup(core.Background(), nil); !r.OK {
		t.Fatal(r.Error())
	}
	defer c.ServiceShutdown(core.Background())
	d := core.MustServiceFor[*Service](c, "downloader")

	good, err := d.Add(mangadex.Manga{ID: "cbe84ea7-2992-4db0-b4ef-d042124f48c8", Title: "Uso-douri: no Kottouya"})
	if err != nil {
		t.Fatal(err)
	}
	bad, _ := d.Add(mangadex.Manga{ID: "9906dfa8-52c9-40ac-8eef-1099a85c8611", Title: "FAIL me"})
	again, _ := d.Add(mangadex.Manga{ID: "cbe84ea7-2992-4db0-b4ef-d042124f48c8", Title: "Uso-douri: no Kottouya"})
	if again.ID != good.ID {
		t.Errorf("adding it twice while queued should be the same job: %d vs %d", again.ID, good.ID)
	}
	for range 2 {
		select {
		case <-changed:
		case <-time.After(10 * time.Second):
			t.Fatalf("downloads didn't finish: %+v", d.Jobs())
		}
	}
	jobs := map[int]Job{}
	for _, j := range d.Jobs() {
		jobs[j.ID] = j
	}
	if j := jobs[good.ID]; j.State != "done" || j.Pages != 2 || j.Volumes != 1 || j.Folder != "Uso-douri- no Kottouya" {
		t.Errorf("good job: %+v\nlog: %v", j, d.Log(good.ID))
	}
	if j := jobs[bad.ID]; j.State != "failed" || !strings.Contains(j.Error, "no chapters") {
		t.Errorf("bad job: %+v", j)
	}
	series, _ := library.Library{Dir: lib}.Series(good.Folder)
	if len(series.Books) != 1 || series.MangaDex != "cbe84ea7-2992-4db0-b4ef-d042124f48c8" {
		t.Errorf("on the shelf: %+v", series)
	}
	// Adding it again now it's there is a check for new chapters, in the same folder.
	up, err := d.Add(mangadex.Manga{ID: "cbe84ea7-2992-4db0-b4ef-d042124f48c8", Title: "Uso-douri: no Kottouya"})
	if err != nil || up.Folder != good.Folder || up.ID == good.ID {
		t.Errorf("re-add: %+v %v", up, err)
	}
	// A different series with the same name gets its own folder.
	other, _ := d.Add(mangadex.Manga{ID: "00000000-0000-0000-0000-000000000000", Title: "Uso-douri: no Kottouya"})
	if other.Folder != "Uso-douri- no Kottouya (2)" {
		t.Errorf("namesake folder %q", other.Folder)
	}
	if _, err := d.Update("not downloaded"); err == nil {
		t.Error("Update of a folder without manga.json should fail")
	}
}
