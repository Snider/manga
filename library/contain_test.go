// SPDX-License-Identifier: EUPL-1.2

package library

import (
	"os"
	"path/filepath"
	"testing"
)

// A series folder that's a symlink to somewhere outside the bookcase can't be
// read through the library: the go-io medium keeps everything inside.
func TestSymlinkOutOfTheBookcase(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "cover.jpg"), []byte("not yours"), 0o644)
	lib := Library{Dir: t.TempDir()}
	if err := os.Symlink(outside, filepath.Join(lib.Dir, "Sneaky")); err != nil {
		t.Skip("no symlinks here:", err) // Windows without developer mode
	}
	if b, err := lib.Cover("Sneaky"); err == nil {
		t.Fatalf("read %q from outside the bookcase", b)
	}
	if err := lib.WriteInfo("Sneaky", Info{Title: "x"}); err == nil {
		t.Fatal("wrote outside the bookcase")
	}
	if _, err := os.Stat(filepath.Join(outside, "manga.json")); err == nil {
		t.Fatal("manga.json landed outside")
	}
}
