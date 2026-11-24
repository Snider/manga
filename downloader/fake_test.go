// SPDX-License-Identifier: EUPL-1.2

package downloader

import (
	"os"
	"testing"

	"manga/internal/fakedl"
)

// The test binary doubles as mangadex-dl (see internal/fakedl).
func TestMain(m *testing.M) {
	fakedl.Run()
	os.Exit(m.Run())
}

func fakeTool(t *testing.T) string { return fakedl.Tool(t) }
