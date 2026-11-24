# Manga Shelf

A Windows manga manager for Snider's friend. She's not a developer, so **nothing may need a terminal**. Wraps mangadex-dl (mansuf/mangadex-downloader, MIT).

Second simple real consumer of CoreGO after GrootDPS (`../groot-dps`, the same shape). Use CoreGO packages where they do a real job: core (services, bus, CLI), go-process (running mangadex-dl), go-store (settings and progress). A CoreGO bug found here gets fixed in CoreGO, not shimmed here.

## Rules
- Words on the page are for a non-technical reader: plain, friendly, no jargon. Errors say what to do next.
- The library folder is hers: never delete or rename anything in it. We only add `manga.json` to series we download.
- Every name from the page goes through `library.ValidName` and `os.Root`. Nothing escapes the shelf.
- mangadex-dl exits 0 even when it fails. Read its `[ERROR]` lines (`downloader.Parse`). It logs to stderr.
- `--path` is the series folder itself (mangadex-dl doesn't add a title folder).
- The pinned mangadex-dl version and its SHA-256 live in `downloader/tool.go` and `.github/workflows/release.yml`. Bump them together.
- `./check.sh` before committing. Every source file starts with the SPDX EUPL-1.2 header.
- Dev preview in a browser: `MANGASHELF_PREVIEW=<library dir> go test ./cmd/mangashelf -run Preview -timeout 0`, then open http://127.0.0.1:8765.
