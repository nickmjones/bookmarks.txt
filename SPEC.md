# bookmarks.txt — Format Specification

A plain-text format for web bookmarks, inspired by [todo.txt](https://github.com/todotxt/todo.txt).

The design goal is the same as todo.txt's: a file you can read, edit, grep, sort, and
diff with ordinary tools, with no database and no required application. A bookmark is just
a line of text.

## Design principles

- **Plain UTF-8 text**, one bookmark per line. No multi-line records.
- **Greppable and sortable** with standard Unix tools (`grep`, `sort`, `awk`).
- **The URL is the identity** of a bookmark (after normalization — see *Deduplication*).
- **Optional everything except the URL.** A bare URL on a line is a valid bookmark.
- **Inline, extensible metadata** via `key:value` pairs, like todo.txt.

## Line format

```
URL ["Title"] [#folder] [[notes]] [key:value ...]
```

A line is a whitespace-separated sequence of fields. Only the URL is required. The
canonical field order is:

```
URL  "Title"  #folder  [notes]  added:YYYY-MM-DD
```

Parsers SHOULD NOT depend on field order beyond the URL coming first — every non-URL
field is self-identifying by its delimiter or sigil, so order is a convention, not a
requirement.

### Fields

| Field    | Delimiter / sigil     | Required | Description |
|----------|-----------------------|----------|-------------|
| URL      | none (first token)    | **yes**  | The bookmark's address and its identity. Bare, unquoted, contains no spaces. |
| Title    | `"…"` double quotes   | no       | The human-readable page title. Quoted because it contains spaces. Machine-fillable (a tool may fetch the page `<title>`). |
| Folder   | `#` prefix            | no       | A single category / folder for the bookmark. One token, no spaces. |
| Notes    | `[…]` square brackets | no       | Freeform commentary — why you saved it, what it's for. |
| added    | `added:` key:value    | no       | The date you saved the bookmark, in ISO 8601 (`YYYY-MM-DD`). |

### Why these delimiters

- **URL first and unquoted** makes extraction trivial (`awk '{print $1}'`) and
  unambiguous, since a URL never contains a space.
- **Title in `"…"`** separates the human label from the sigil/metadata zone so a parser
  knows where it ends.
- **Notes in `[…]`** rather than quotes: brackets are *distinct* open/close delimiters,
  so a note may freely contain apostrophes and quotation marks (the characters most
  common in prose) without escaping. The bracket is self-delimiting, so notes need no
  sigil of their own.
- **`#` for folder** (todo.txt uses `+` for projects; this format repurposes `#` for the
  single folder a bookmark lives in).
- **ISO 8601 dates** sort chronologically as plain text — the whole reason todo.txt dates
  are useful. `2026-06-16`, never `6-16-26`.

### Grammar (informal)

```
line     = url SP *(field SP) [field]
url      = <non-whitespace token, the first field>
field    = title / folder / notes / keyval
title    = '"' <any char except unescaped '"'> '"'
folder   = '#' <non-whitespace token>
notes    = '[' <any char except unescaped ']'> ']'
keyval   = key ':' value          ; e.g. added:2026-06-16
```

A literal `]` inside a note, or `"` inside a title, may be escaped with a backslash.
Both are rare in practice.

## Examples

```
https://www.apple.com "Apple" #technology [makes computers and operating systems] added:2026-06-16
https://github.com/charmbracelet "Charmbracelet" [produces tools for making terminal apps] added:2026-06-16
https://danluu.com/input-lag/
```

The first line uses every field. The second omits the folder. The third is a bare URL —
still valid.

## Conventions and tooling notes

These are recommendations for tools that read or write the file; they are not part of the
on-disk format.

### Deduplication

The format itself does not deduplicate — like todo.txt, the file is dumb and the tool is
smart. Deduplication is an **add-time tool behavior**:

1. Normalize the incoming URL — lowercase the host, strip a trailing `/`, drop tracking
   params (`utm_*`, etc.), optionally drop the fragment.
2. Compare against existing normalized URLs.
3. On a match, either **reject** the new entry or **merge** it (keep the existing line,
   fold in any new folder/notes). Merging is usually more useful, since re-saving a URL
   often comes with a fresh reason.

Unlike todos — which have no natural identity and so should never be deduped — bookmarks
have one: the normalized URL.

### Sorting

Because dates are ISO 8601 and stored as `added:` values, chronological sort is:

```
sort -t: -k2 bookmarks.txt    # rough; assumes added: is the line's only key:value
```

### Possible future fields

Not adopted, but natural extensions if needed (link rot is the concern todo.txt never
had):

- `status:dead` / `status:moved` — mark a URL that no longer resolves.
- `archive:https://web.archive.org/…` — a snapshot to fall back on.
- `pub:YYYY` — the content's original publication date, distinct from `added:`.
- `via:…` — provenance (where you found the link).

## Relationship to todo.txt

This format borrows todo.txt's plain-text, one-record-per-line, `key:value` philosophy,
but diverges where bookmarks differ from tasks:

- A bookmark's payload is a **structured URL**, not free text — so the URL is anchored
  first and is the identity key.
- Bookmarks have **no "done" state**; there is no completion marker. (The relevant
  lifecycle is unread → read and eventual link rot, not open → complete.)
- The URL gives bookmarks a **natural identity**, which both enables and obligates a
  deduplication policy — something todo.txt deliberately avoids for tasks.
