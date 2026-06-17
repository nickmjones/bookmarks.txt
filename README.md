# bookmarks.txt

A plain-text format for web bookmarks, in the spirit of [todo.txt](https://github.com/todotxt/todo.txt).

One bookmark per line. No database, no lock-in, no app required — just a text
file you can read, edit, `grep`, `sort`, and version with git. This repo is the
**format** ([`SPEC.md`](SPEC.md)) plus two small reference implementations that
share the same file:

- **`bm.py`** — a zero-dependency capture service: a web browse UI plus a
  bookmarklet to save the page you're on. Pure Python 3 standard library.
- **`bmtui`** — a [Charm](https://github.com/charmbracelet)/Bubble Tea terminal
  UI for browsing and curating: folder sidebar, fuzzy filter, clipboard capture,
  inline edit/delete, and live reload.

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

## Install

`bm.py` needs nothing but Python 3 — run it straight from a checkout. The
terminal UI is a Go program; install it onto your `PATH` with any of:

```sh
# 1. Go users — installs `bmtui` from source, no checkout needed
go install github.com/nickmjones/bookmarks.txt/bmtui@latest

# 2. From a checkout, via the Makefile (copies to ~/.local/bin)
git clone https://github.com/nickmjones/bookmarks.txt && cd bookmarks.txt
make install            # override the target dir with `make install BINDIR=/usr/local/bin`

# 3. Prebuilt binaries — grab one from the Releases page (no Go needed)
#    tagged releases publish linux/macOS/windows × amd64/arm64 via GoReleaser
```

## Where your bookmarks live

Both tools resolve the bookmarks file the same way, so the service and the TUI
always agree on one file:

1. `$BM_FILE` if set, else
2. `./bookmarks.txt` when you're inside a project that has one, else
3. the per-user default `~/.config/bmtui/bookmarks.txt` (respecting
   `$XDG_CONFIG_HOME`), created on first write.

## Capture: service + bookmarklet

```sh
python3 bm.py        # serves http://127.0.0.1:8888
```

Open <http://127.0.0.1:8888> and **drag the "+ bookmark" link to your bookmarks
bar**. Click it on any page to save it (URL + title pre-filled; set a folder and
notes, hit Save). Re-saving a URL updates the existing entry instead of
duplicating it.

Config via env: `BM_FILE` (see [above](#where-your-bookmarks-live)), `BM_HOST`
(default `127.0.0.1`), `BM_PORT` (default `8888`). It ships with **no auth** —
run it on localhost or behind a VPN, not the public internet.

## Browse & curate: the TUI

```sh
bmtui                # or `cd bmtui && go build -o bmtui . && ./bmtui` from a checkout
```

Two panes: a **folder sidebar** on the left and the bookmark list on the right.
Selecting a folder filters the list (`All` and `(none)` are always available,
with counts). It polls the file once a second, so bookmarks saved via the
bookmarklet or the service appear automatically while it's open.

Keys:

- `tab` / `h` / `l` — move focus between the folder sidebar and the list
- `j` / `k` (or `↑` / `↓`) — move within the focused pane; `g` / `G` jump to
  top / bottom of the folder list
- `/` — fuzzy-filter the current folder's bookmarks
- `enter` — open the highlighted bookmark in your browser
- `ctrl+k` — add a bookmark from the clipboard
- `e` — edit · `d` — delete · `q` — quit

`ctrl+k` (add) and `e` (edit) open a **floating palette** over the UI. Within it,
`tab` / `↓` and `shift+tab` / `↑` move between fields (wrapping), `enter` saves,
`esc` cancels. Adds de-dupe/merge against existing bookmarks just like the
service does.

> Clipboard capture (`ctrl+k`) shells out to the system clipboard: macOS uses
> the built-in `pbpaste`; Linux needs `wl-clipboard` (Wayland) or `xclip`/`xsel`
> (X11).

## Format a file

Both tools can rewrite a file in canonical form (a `fmt` for bookmarks) — handy
for tidying hand-edited files:

```sh
python3 bm.py --reformat bookmarks.txt
bmtui --reformat bookmarks.txt
```

The two implementations are checked for byte-identical output in CI.

## Design notes

- **Dumb file, smart tools.** The file is the source of truth; each tool is a
  thin lens over it. Any number of front-ends can interoperate because the file
  is a neutral contract.
- **De-dup belongs to the tools, not the format.** Unlike a todo, a bookmark has
  a natural identity (its normalized URL), so adds de-dupe/merge rather than
  duplicate. URL normalization is identical in `bm.py` and `bmtui`.
- **Two parsers, one format.** `bm.py` and `bmtui/main.go` each implement the
  format independently; their output is verified byte-identical
  (`bmtui/format_test.go`). Keep them in sync if the format changes.

## Repo layout

```
SPEC.md                 the format specification
bm.py                   capture service + web UI + bookmarklet
bmtui/                  bmtui — Bubble Tea browse/curate TUI (Go module)
Makefile                build / install / test
bookmarks.example.txt   sample data
```

## Development

```sh
make build      # build ./bmtui/bmtui
make test       # run the Go test suite
make install    # build + copy to ~/.local/bin (BINDIR overridable)
```

CI runs vet, the test suite, and the Go-vs-Python format parity check on every
push and pull request.

## License

[MIT](LICENSE) © 2026 Nick Jones
