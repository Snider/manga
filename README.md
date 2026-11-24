# Manga Shelf 📚

A bookcase for manga on Windows. Search MangaDex, click **Put it on my shelf**, and it downloads volume by volume into a folder. Read it in the window, and it remembers your page. No terminal needed.

It's a friendly face on [mangadex-dl](https://github.com/mansuf/mangadex-downloader) (MIT), which does the downloading.

## Using it

1. Download the `.zip` from [Releases](https://github.com/Snider/manga/releases), unzip it anywhere, and double-click `MangaShelf.exe`.
2. **Find manga**: type a title, or paste a MangaDex link.
3. **Bookcase**: click a cover to read. Manga reads right to left, so click the left side or press ← for the next page. Tick off "Right to left" for comics.
4. **Check for new chapters**: this works on one series or all of them. It fetches only what's new.

Everything lives in `Documents\Manga`, one folder per series, so you can change that in **Settings**. Folders of `.cbz` files you already have show up on the shelf too. PDFs and EPUBs open in your usual reader.

MangaDex is run by volunteers who scanlate. Big licensed series are often taken down there. If you love a series, buy the official volumes.

## Terminal, if you want it

```
MangaShelf search frieren
MangaShelf get https://mangadex.org/title/<id>
MangaShelf shelf
MangaShelf update
```

## How it's built

Go and [CoreGO](https://github.com/dappcore), in a Wails window:

| Package | Job |
|---|---|
| `mangadex` | Searches the MangaDex API and fetches covers |
| `downloader` | A Core service: runs mangadex-dl one series at a time through go-process and turns its log into progress |
| `library` | The folder is the bookcase: scans series, reads `.cbz` pages, and never lets a name step outside the folder |
| `shelf` | go-store (SQLite): settings, plus your page in every book |
| `cmd/mangashelf` | The window, the page, and the terminal commands |

The page talks to the program inside the process: no port is opened. The only network traffic is to MangaDex, plus GitHub once if mangadex-dl isn't next to the program. That download is pinned and checked against its checksum.

`./check.sh` before committing. Licence: EUPL-1.2.
