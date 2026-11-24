// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"archive/zip"

	core "github.com/dappcore/go"
	"github.com/dappcore/go-io/go/local"
)

// The mangadex-dl Windows build we fetch when it isn't next to the program,
// pinned: a download that doesn't match the checksum is thrown away.
// (MIT, github.com/mansuf/mangadex-downloader.)
const (
	ToolVersion = "3.1.4"
	toolURL     = "https://github.com/mansuf/mangadex-downloader/releases/download/v3.1.4/mangadex-dl_x64_v3.1.4.zip"
	toolSHA256  = "5c694bd011947dcb89914070718948d111d3f1873b5fef1ddbf00d6a286f5bcd"
	toolMaxZip  = 200 << 20
)

// FindTool looks for mangadex-dl: the setting if there is one, then a
// mangadex-dl folder next to the program (how the release ships), then the
// copy we fetched into dataDir, then the PATH (pip install mangadex-downloader).
// "" means it needs fetching.
func FindTool(setting, dataDir string) string {
	if setting != "" && core.Stat(setting).OK {
		return setting
	}
	exe := "mangadex-dl"
	if core.OS() == "windows" {
		exe += ".exe"
	}
	var candidates []string
	if self := core.Executable(); self.OK {
		candidates = append(candidates, core.PathJoin(core.PathDir(self.Value.(string)), "mangadex-dl", exe))
	}
	candidates = append(candidates, core.PathJoin(dataDir, "tools", "mangadex-dl", exe))
	for _, c := range candidates {
		if core.Stat(c).OK {
			return c
		}
	}
	if found := (core.App{}).Find("mangadex-dl", "mangadex-dl"); found.OK {
		return found.Value.(*core.App).Path
	}
	return ""
}

// CanFetch: is there a build we can fetch for this computer? (Windows only:
// elsewhere it's pip install mangadex-downloader.)
func CanFetch() bool { return core.OS() == "windows" && core.Arch() == "amd64" }

// FetchTool downloads the pinned mangadex-dl into dataDir/tools/mangadex-dl
// and returns the exe's path. status gets a line now and then for the page.
func FetchTool(ctx core.Context, dataDir string, status func(string)) (string, error) {
	if !CanFetch() {
		return "", core.E("downloader.FetchTool", core.Sprintf("there's no mangadex-dl download for %s/%s: pip install mangadex-downloader", core.OS(), core.Arch()), nil)
	}
	status("Downloading mangadex-dl " + ToolVersion + " (about 30 MB, once)…")
	ctx, cancel := core.WithTimeout(ctx, 15*core.Minute)
	defer cancel()
	req := core.NewHTTPRequestContext(ctx, "GET", toolURL, nil)
	if !req.OK {
		return "", req.Err()
	}
	resp, err := new(core.HTTPClient).Do(req.Value.(*core.Request))
	if err != nil {
		return "", core.E("downloader.FetchTool", "couldn't download mangadex-dl", err)
	}
	if resp.StatusCode != core.StatusOK {
		resp.Body.Close()
		return "", core.E("downloader.FetchTool", "couldn't download mangadex-dl: GitHub answered "+resp.Status, nil)
	}
	body := core.ReadAll(core.LimitReader(resp.Body, toolMaxZip)) // closes the body
	if !body.OK {
		return "", core.E("downloader.FetchTool", "couldn't download mangadex-dl", body.Err())
	}
	status("Checking and unpacking mangadex-dl…")
	return installTool([]byte(body.Value.(string)), toolSHA256, core.PathJoin(dataDir, "tools"))
}

// installTool checks the zip against its checksum and unpacks it into dir
// (replacing what was there). The zip holds one mangadex-dl/ folder, and it's
// unpacked through a go-io medium rooted at dir: nothing in it can write
// anywhere else.
func installTool(body []byte, sha string, dir string) (string, error) {
	if got := core.SHA256Hex(body); got != sha {
		return "", core.E("downloader.installTool", "the mangadex-dl download didn't match its checksum (got "+got[:12]+"…): not using it", nil)
	}
	z, err := zip.NewReader(core.NewBufferReader(body), int64(len(body)))
	if err != nil {
		return "", core.E("downloader.installTool", "the mangadex-dl zip is unreadable", err)
	}
	if r := core.MkdirAll(dir, 0o755); !r.OK {
		return "", r.Err()
	}
	m, err := local.New(dir)
	if err != nil {
		return "", err
	}
	defer m.Close()
	const unpack = ".unpack"
	m.DeleteAll(unpack) // a half-finished earlier attempt
	defer m.DeleteAll(unpack)
	if err := m.EnsureDir(unpack); err != nil {
		return "", err
	}
	// The zip unpacks through its own medium, rooted at .unpack: an entry
	// like mangadex-dl/../../evil.exe can't climb out of it.
	um, err := local.New(core.PathJoin(dir, unpack))
	if err != nil {
		return "", err
	}
	defer um.Close()
	for _, f := range z.File {
		name := core.PathToSlash(f.Name)
		if !core.HasPrefix(name, "mangadex-dl/") || climbs(name) {
			return "", core.E("downloader.installTool", "unexpected file in the mangadex-dl zip: "+f.Name, nil)
		}
		if f.FileInfo().IsDir() {
			if err := um.EnsureDir(core.TrimSuffix(name, "/")); err != nil {
				return "", err
			}
			continue
		}
		if err := copyOut(um, name, f); err != nil {
			return "", err
		}
	}
	um.Close() // before the rename: Windows won't move a folder that's held open
	if !m.IsFile(unpack + "/mangadex-dl/mangadex-dl.exe") {
		return "", core.E("downloader.installTool", "the mangadex-dl zip has no mangadex-dl.exe", nil)
	}
	m.DeleteAll("mangadex-dl")
	if err := m.Rename(unpack+"/mangadex-dl", "mangadex-dl"); err != nil {
		return "", err
	}
	return core.PathJoin(dir, "mangadex-dl", "mangadex-dl.exe"), nil
}

// climbs: does a zip entry name try to step up a folder? The medium would
// keep it inside anyway; a zip that tries is refused outright.
func climbs(name string) bool {
	for _, part := range core.Split(name, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func copyOut(m *local.Medium, name string, f *zip.File) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := m.Create(name)
	if err != nil {
		return err
	}
	copied := core.Copy(w, core.LimitReader(r, toolMaxZip))
	if cerr := w.Close(); copied.OK && cerr != nil {
		return cerr
	}
	if !copied.OK {
		return copied.Err()
	}
	return nil
}
