# bookmarks.txt

A plain-text format for web bookmarks, in the spirit of [todo.txt](https://github.com/todotxt/todo.txt).

One bookmark per line. No database, no lock-in, no app required — just a text
file you can read, edit, `grep`, `sort`, and version with git. This repo is the
**format** ([`SPEC.md`](SPEC.md)) plus two small reference implementations that
share the same file:

- **`bm.py`** — a tiny zero-dependency capture service + web browse UI, with a
  bookmarklet to save the page you're on.
- **`tui/`** (`bmtui`) — a [Charm](https://github.com/charmbracelet)/Bubble Tea
  terminal UI for browsing and curating, with live reload.

## The format

```
URL  "Title"  #folder  [notes]  added:YYYY-MM-DD
```

Only the URL is required; everything else is optional and order is just a
convention. Example:

```
https://www.apple.com "Apple" #technology [makes computers and operating systems] added:2026-06-16
https://danluu.com/input-lag/
```

- **URL** — the address, and the bookmark's identity (used for de-duplication).
- **`"Title"`** — human-readable label (quoted, so it can contain spaces).
- **`#folder`** — a single category.
- **`[notes]`** — freeform commentary. Brackets are distinct open/close
  delimiters, so notes can contain quotes and apostrophes without escaping.
- **`added:YYYY-MM-DD`** — ISO 8601 so it sorts chronologically as plain text.

See [`SPEC.md`](SPEC.md) for the full specification, including escaping, the
de-duplication policy, and possible future fields.

## Quick start

```sh
git clone <your-repo-url> bookmarks && cd bookmarks
cp bookmarks.example.txt bookmarks.txt   # your own bookmarks live here (gitignored)
```

### Capture service + bookmarklet

```sh
python3 bm.py        # serves http://127.0.0.1:8888 (Python 3, stdlib only)
```

Open <http://127.0.0.1:8888> and **drag the "+ bookmark" link to your bookmarks
bar**. Click it on any page to save it (URL + title pre-filled; set a folder and
notes, hit Save). Re-saving a URL updates the existing entry instead of
duplicating it.

Config via env: `BM_FILE` (default `./bookmarks.txt`), `BM_HOST`
(default `127.0.0.1`), `BM_PORT` (default `8888`). It ships with **no auth** —
run it on localhost or behind a VPN, not the public internet.

### Terminal UI

```sh
cd tui
go build -o bmtui .
./bmtui              # run from a dir where it can find bookmarks.txt, or set BM_FILE
```

Keys: `↑/↓` or `j/k` move · `/` filter · `enter` open in browser · `e` edit ·
`d` delete · `q` quit. It polls the file once a second, so bookmarks saved via
the bookmarklet appear automatically while it's open.

Tagged releases (`vX.Y.Z`) publish prebuilt `bmtui` binaries via GoReleaser, so
you don't need Go installed to use the TUI — grab one from the Releases page.

### Format a file

Both tools can rewrite a file in canonical form (a `fmt` for bookmarks) — handy
for tidying hand-edited files:

```sh
python3 bm.py --reformat bookmarks.txt
tui/bmtui --reformat bookmarks.txt
```

The two implementations are checked for byte-identical output in CI.

## Design notes

- **Dumb file, smart tools.** The file is the source of truth; each tool is a
  thin lens over it. Any number of front-ends can interoperate because the file
  is a neutral contract.
- **De-dup belongs to the tools, not the format.** Unlike a todo, a bookmark has
  a natural identity (its normalized URL), so the service de-dupes/merges on add.
- **Two parsers, one format.** `bm.py` and `tui/main.go` each implement the
  format independently; their output is verified byte-identical
  (`tui/format_test.go`). Keep them in sync if the format changes.

## Repo layout

```
SPEC.md                 the format specification
bm.py                   capture service + web UI + bookmarklet
tui/                    bmtui — Bubble Tea browse/curate TUI (Go module)
bookmarks.example.txt   sample data; copy to bookmarks.txt to start
```

## License

[MIT](LICENSE) © 2026 Nick Jones
