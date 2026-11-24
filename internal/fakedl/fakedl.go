// SPDX-License-Identifier: EUPL-1.2

// Package fakedl lets a test binary stand in for mangadex-dl, so the whole
// download path runs in tests on every OS. A package's TestMain calls Run
// first; a test passes Tool(t) as the mangadex-dl path.
//
//	func TestMain(m *testing.M) { fakedl.Run(); os.Exit(m.Run()) }
//	set.Tool = fakedl.Tool(t)
//
// It logs like the real tool, to stderr, and writes "Vol. 1.cbz" into
// --path. In the folder name: FAIL logs the real tool's "no chapters" error
// (exiting 0, as it does); SLOW takes ten seconds over it (to be stopped).
package fakedl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const env = "PINKU_FAKE_MANGADEX_DL"

// Run plays mangadex-dl and exits, when this process was started as one.
func Run() {
	if os.Getenv(env) == "1" {
		os.Exit(play(os.Args[1:]))
	}
}

// Tool is the path to use as mangadex-dl: this test binary, told to play it
// (children inherit the environment).
func Tool(t testing.TB) string {
	t.Helper()
	t.Setenv(env, "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func play(args []string) int {
	dir := ""
	for i, a := range args {
		if a == "--path" && i+1 < len(args) {
			dir = args[i+1]
		}
	}
	say := func(s string) { fmt.Fprintln(os.Stderr, s) }
	say("[INFO] Checking url")
	if strings.Contains(dir, "FAIL") {
		say("[ERROR] Manga 'x' with en language has no chapters")
		return 0
	}
	say("[INFO] Downloading [grp] Volume. 1 Chapter. 1 page 1")
	if strings.Contains(dir, "SLOW") {
		time.Sleep(10 * time.Second)
	}
	say("[INFO] Downloading [grp] Volume. 1 Chapter. 1 page 2")
	say("[INFO] Vol. 1 has finished download, converting chapters to .cbz file...")
	if err := os.WriteFile(filepath.Join(dir, "Vol. 1.cbz"), []byte("cbz"), 0o644); err != nil {
		say("[ERROR] " + err.Error())
		return 1
	}
	say(`[INFO] Download finished for manga "x"`)
	return 0
}
