// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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
	if setting != "" {
		if _, err := os.Stat(setting); err == nil {
			return setting
		}
	}
	exe := "mangadex-dl"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	var candidates []string
	if self, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(self), "mangadex-dl", exe))
	}
	candidates = append(candidates, filepath.Join(dataDir, "tools", "mangadex-dl", exe))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if p, err := exec.LookPath("mangadex-dl"); err == nil {
		return p
	}
	return ""
}

// CanFetch: is there a build we can fetch for this computer? (Windows only:
// elsewhere it's pip install mangadex-downloader.)
func CanFetch() bool { return runtime.GOOS == "windows" && runtime.GOARCH == "amd64" }

// FetchTool downloads the pinned mangadex-dl into dataDir/tools/mangadex-dl
// and returns the exe's path. status gets a line now and then for the page.
func FetchTool(ctx context.Context, dataDir string, status func(string)) (string, error) {
	if !CanFetch() {
		return "", fmt.Errorf("there's no mangadex-dl download for %s/%s: pip install mangadex-downloader", runtime.GOOS, runtime.GOARCH)
	}
	status("Downloading mangadex-dl " + ToolVersion + " (about 30 MB, once)…")
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, toolURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("couldn't download mangadex-dl: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("couldn't download mangadex-dl: GitHub answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, toolMaxZip))
	if err != nil {
		return "", fmt.Errorf("couldn't download mangadex-dl: %w", err)
	}
	status("Checking and unpacking mangadex-dl…")
	return installTool(body, toolSHA256, filepath.Join(dataDir, "tools"))
}

// installTool checks the zip against its checksum and unpacks it into dir
// (replacing what was there). The zip holds one mangadex-dl/ folder.
func installTool(body []byte, sha string, dir string) (string, error) {
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != sha {
		return "", fmt.Errorf("the mangadex-dl download didn't match its checksum (got %s…): not using it", got[:12])
	}
	z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, ".unpack-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	root, err := os.OpenRoot(tmp) // nothing in the zip can write outside it
	if err != nil {
		return "", err
	}
	defer root.Close()
	for _, f := range z.File {
		name := filepath.ToSlash(f.Name)
		if !strings.HasPrefix(name, "mangadex-dl/") {
			return "", fmt.Errorf("unexpected file in the mangadex-dl zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := root.MkdirAll(strings.TrimSuffix(name, "/"), 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := root.MkdirAll(filepath.ToSlash(filepath.Dir(name)), 0o755); err != nil {
			return "", err
		}
		if err := copyOut(root, name, f); err != nil {
			return "", err
		}
	}
	exe := filepath.Join(tmp, "mangadex-dl", "mangadex-dl.exe")
	if _, err := os.Stat(exe); err != nil {
		return "", fmt.Errorf("the mangadex-dl zip has no mangadex-dl.exe")
	}
	final := filepath.Join(dir, "mangadex-dl")
	os.RemoveAll(final)
	if err := os.Rename(filepath.Join(tmp, "mangadex-dl"), final); err != nil {
		return "", err
	}
	return filepath.Join(final, "mangadex-dl.exe"), nil
}

func copyOut(root *os.Root, name string, f *zip.File) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, io.LimitReader(r, toolMaxZip)); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}
