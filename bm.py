#!/usr/bin/env python3
"""bm.py — a tiny self-hosted service over a bookmarks.txt file.

The file is the source of truth; this is the "smart tool" over the "dumb file".
Zero dependencies (Python stdlib only). Designed to run on localhost or behind a
VPN, so it ships with no authentication.

Routes:
  GET  /              browse UI (searchable table of all bookmarks)
  GET  /add?url=&title=   add/confirm form, pre-filled, shows merge notice
  POST /add          normalize -> dedupe/merge -> append, atomic write
  GET  /bookmarks.txt    the raw file

Run:
  python3 bm.py                      # serves ./bookmarks.txt on 127.0.0.1:8888
  BM_FILE=/path/to/bookmarks.txt BM_PORT=9000 python3 bm.py

See SPEC.md for the line format.
"""

import datetime
import html
import os
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import (parse_qs, parse_qsl, urlencode, urlsplit, urlunsplit)

HERE = os.path.dirname(os.path.abspath(__file__))
FILE = os.environ.get("BM_FILE", os.path.join(HERE, "bookmarks.txt"))
HOST = os.environ.get("BM_HOST", "127.0.0.1")
PORT = int(os.environ.get("BM_PORT", "8888"))

LOCK = threading.Lock()

# Query parameters stripped during URL normalization (tracking junk).
TRACKING_PARAMS = {
    "utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content",
    "fbclid", "gclid", "dclid", "msclkid", "mc_cid", "mc_eid",
    "ref", "ref_src", "ref_url", "igshid", "_hsenc", "_hsmi",
}


# ---------------------------------------------------------------------------
# Parsing / serialization  (see SPEC.md)
#   URL  "Title"  #folder  [notes]  added:YYYY-MM-DD
# ---------------------------------------------------------------------------

def _read_delimited(s, start, close_c):
    """Read s[start]==open .. matching close_c, honoring backslash escapes."""
    i = start + 1
    out = []
    while i < len(s):
        c = s[i]
        if c == "\\" and i + 1 < len(s):
            out.append(s[i + 1])
            i += 2
            continue
        if c == close_c:
            return "".join(out), i + 1
        out.append(c)
        i += 1
    return "".join(out), i  # unterminated: take the rest


def parse_line(line):
    """Parse one bookmarks.txt line into a record dict, or None if blank.

    Field order is not assumed beyond the URL coming first; every other field
    is identified by its delimiter/sigil.
    """
    s = line.strip()
    if not s:
        return None
    head, _, rest = s.partition(" ")
    rec = {"url": head, "title": None, "folder": None, "notes": None,
           "added": None, "extra": [], "stray": []}
    i, n = 0, len(rest)
    while i < n:
        c = rest[i]
        if c.isspace():
            i += 1
            continue
        if c == '"':
            rec["title"], i = _read_delimited(rest, i, '"')
        elif c == "[":
            rec["notes"], i = _read_delimited(rest, i, "]")
        elif c == "#":
            j = i + 1
            while j < n and not rest[j].isspace():
                j += 1
            rec["folder"] = rest[i + 1:j]
            i = j
        else:
            j = i
            while j < n and not rest[j].isspace():
                j += 1
            tok = rest[i:j]
            i = j
            if ":" in tok and not tok.startswith(":"):
                k, v = tok.split(":", 1)
                if k == "added":
                    rec["added"] = v
                else:
                    rec["extra"].append((k, v))  # preserve unknown key:values
            else:
                rec["stray"].append(tok)  # preserve anything we don't model
    return rec


def _esc(val, close_c):
    return val.replace("\\", "\\\\").replace(close_c, "\\" + close_c)


def format_line(rec):
    parts = [rec["url"]]
    if rec.get("title"):
        parts.append('"%s"' % _esc(rec["title"], '"'))
    if rec.get("folder"):
        parts.append("#" + rec["folder"])
    if rec.get("notes"):
        parts.append("[%s]" % _esc(rec["notes"], "]"))
    if rec.get("added"):
        parts.append("added:" + rec["added"])
    for k, v in rec.get("extra", []):
        parts.append("%s:%s" % (k, v))
    parts.extend(rec.get("stray", []))
    return " ".join(parts)


# ---------------------------------------------------------------------------
# URL normalization + storage
# ---------------------------------------------------------------------------

def normalize_url(u):
    """Canonical form used as the dedup identity key."""
    try:
        sp = urlsplit(u.strip())
    except ValueError:
        return u.strip().lower()
    if not sp.netloc:  # not a real URL; compare verbatim-ish
        return u.strip().lower()
    scheme = (sp.scheme or "https").lower()
    host = (sp.hostname or "").lower()
    netloc = host
    if sp.port and not ((scheme == "http" and sp.port == 80) or
                        (scheme == "https" and sp.port == 443)):
        netloc = "%s:%d" % (host, sp.port)
    path = sp.path
    if path == "":
        path = "/"  # treat bare host and host/ as the same bookmark
    elif len(path) > 1 and path.endswith("/"):
        path = path.rstrip("/")
    q = [(k, v) for k, v in parse_qsl(sp.query, keep_blank_values=True)
         if k.lower() not in TRACKING_PARAMS]
    return urlunsplit((scheme, netloc, path, urlencode(q), ""))  # drop fragment


def today():
    return datetime.date.today().isoformat()


def load():
    recs = []
    if os.path.exists(FILE):
        with open(FILE, encoding="utf-8") as f:
            for line in f:
                r = parse_line(line)
                if r:
                    recs.append(r)
    return recs


def save(recs):
    tmp = FILE + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        for r in recs:
            f.write(format_line(r) + "\n")
    os.replace(tmp, FILE)  # atomic: a crash never leaves a partial file


def find_existing(recs, url):
    norm = normalize_url(url)
    for r in recs:
        if normalize_url(r["url"]) == norm:
            return r
    return None


def add_or_merge(url, title, folder, notes):
    """Append a new bookmark, or merge into an existing one with the same URL.

    Merge policy: keep the original line and its added: date; fill the title
    only if it was empty (don't clobber a curated title with a raw page title);
    update folder/notes when the form supplied a value.
    """
    with LOCK:
        recs = load()
        existing = find_existing(recs, url)
        if existing:
            if title and not existing.get("title"):
                existing["title"] = title
            if folder:
                existing["folder"] = folder
            if notes:
                existing["notes"] = notes
            save(recs)
            return "updated", existing
        rec = {"url": url, "title": title or None, "folder": folder or None,
               "notes": notes or None, "added": today(), "extra": [], "stray": []}
        recs.append(rec)
        save(recs)
        return "added", rec


# ---------------------------------------------------------------------------
# HTML
# ---------------------------------------------------------------------------

PAGE_CSS = """
:root { color-scheme: light dark; }
body { font: 15px/1.5 system-ui, sans-serif; margin: 0 auto; max-width: 60rem;
       padding: 1.5rem; }
h1 { font-size: 1.3rem; }
a { color: inherit; }
input[type=text], input[type=url] { width: 100%; box-sizing: border-box;
       padding: .5rem; font: inherit; }
table { border-collapse: collapse; width: 100%; margin-top: 1rem; }
th, td { text-align: left; padding: .4rem .6rem; border-bottom: 1px solid #8884;
         vertical-align: top; }
th { cursor: pointer; user-select: none; }
.folder { font-size: .8rem; opacity: .75; white-space: nowrap; }
.notes { opacity: .8; }
.added { opacity: .6; white-space: nowrap; font-variant-numeric: tabular-nums; }
.muted { opacity: .6; }
.bm { display: inline-block; padding: .4rem .8rem; border: 1px dashed #8888;
      border-radius: .4rem; text-decoration: none; }
form label { display: block; margin: .8rem 0 .2rem; font-weight: 600; }
button { font: inherit; padding: .5rem 1rem; margin-top: 1rem; cursor: pointer; }
.notice { padding: .6rem .8rem; border-radius: .4rem; background: #ffd60033;
          border: 1px solid #ffd60088; margin: 1rem 0; }
"""


def page(title, body):
    return ("<!doctype html><meta charset=utf-8>"
            "<meta name=viewport content='width=device-width,initial-scale=1'>"
            "<title>%s</title><style>%s</style>%s"
            % (html.escape(title), PAGE_CSS, body))


def browse_page(recs):
    recs = sorted(recs, key=lambda r: (r.get("added") or "", r.get("title") or ""),
                  reverse=True)
    rows = []
    for r in recs:
        url = r["url"]
        label = r.get("title") or url
        folder = ("<span class=folder>#%s</span>" % html.escape(r["folder"])
                  if r.get("folder") else "")
        notes = ("<div class=notes>%s</div>" % html.escape(r["notes"])
                 if r.get("notes") else "")
        rows.append(
            "<tr><td><a href='%s' target=_blank rel=noopener>%s</a>%s"
            "<div class=muted style='font-size:.8rem'>%s</div></td>"
            "<td>%s</td><td class=added>%s</td></tr>"
            % (html.escape(url, quote=True), html.escape(label), notes,
               html.escape(url), folder, html.escape(r.get("added") or "")))
    body = """
<h1>bookmarks <span class=muted>(%d)</span></h1>
<p id=bmwrap class=muted>Drag this to your bookmarks bar:
   <a class=bm id=bmlink href="#">+ bookmark</a></p>
<input type=text id=q placeholder="filter…" autofocus>
<table><thead><tr>
  <th data-k=0>Bookmark</th><th data-k=1>Folder</th><th data-k=2>Added</th>
</tr></thead><tbody id=rows>
%s
</tbody></table>
<script>
// Build the bookmarklet against whatever origin is serving this page.
var bm = "javascript:(function(){window.open('" + location.origin +
  "/add?url='+encodeURIComponent(location.href)+'&title='+" +
  "encodeURIComponent(document.title),'bm','width=540,height=460');})()";
document.getElementById('bmlink').setAttribute('href', bm);
// Live client-side filter across all cell text.
var q = document.getElementById('q'), rows = document.getElementById('rows');
q.addEventListener('input', function(){
  var t = q.value.toLowerCase();
  for (var tr of rows.children)
    tr.style.display = tr.textContent.toLowerCase().indexOf(t) >= 0 ? '' : 'none';
});
// Click a header to sort by that column.
document.querySelectorAll('th').forEach(function(th){
  var asc = true;
  th.addEventListener('click', function(){
    var k = +th.dataset.k, trs = Array.from(rows.children);
    trs.sort(function(a,b){
      var x=a.children[k].textContent.trim(), y=b.children[k].textContent.trim();
      return (x<y?-1:x>y?1:0) * (asc?1:-1);
    });
    asc = !asc; trs.forEach(function(tr){ rows.appendChild(tr); });
  });
});
</script>
""" % (len(recs), "\n".join(rows) or
       "<tr><td colspan=3 class=muted>No bookmarks yet.</td></tr>")
    return page("bookmarks", body)


def add_page(url, title, existing):
    notice = ""
    if existing:
        notice = ("<div class=notice>Already saved%s — submitting will "
                  "update folder/notes.</div>"
                  % (" on " + html.escape(existing["added"])
                     if existing.get("added") else ""))
        title = title or existing.get("title") or ""
        folder = existing.get("folder") or ""
        notes = existing.get("notes") or ""
    else:
        folder = notes = ""
    body = """
<h1>add bookmark</h1>
%s
<form method=post action=/add>
  <label>URL</label>
  <input type=url name=url value="%s" required>
  <label>Title</label>
  <input type=text name=title value="%s">
  <label>Folder</label>
  <input type=text name=folder value="%s" placeholder="technology">
  <label>Notes</label>
  <input type=text name=notes value="%s" placeholder="why this matters">
  <button type=submit>Save</button>
</form>
""" % (notice,
       html.escape(url or "", quote=True),
       html.escape(title or "", quote=True),
       html.escape(folder, quote=True),
       html.escape(notes, quote=True))
    return page("add bookmark", body)


def saved_page(action, rec):
    return page("saved", """
<h1>%s ✓</h1>
<p>%s</p>
<p class=muted>This window closes automatically.</p>
<script>setTimeout(function(){window.close();}, 800);</script>
""" % (html.escape(action), html.escape(rec.get("title") or rec["url"])))


# ---------------------------------------------------------------------------
# HTTP
# ---------------------------------------------------------------------------

class Handler(BaseHTTPRequestHandler):
    def _send(self, body, status=200, ctype="text/html; charset=utf-8"):
        data = body.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        sp = urlsplit(self.path)
        if sp.path == "/":
            self._send(browse_page(load()))
        elif sp.path == "/add":
            q = parse_qs(sp.query)
            url = (q.get("url") or [""])[0]
            title = (q.get("title") or [""])[0]
            existing = find_existing(load(), url) if url else None
            self._send(add_page(url, title, existing))
        elif sp.path == "/bookmarks.txt":
            try:
                with open(FILE, encoding="utf-8") as f:
                    self._send(f.read(), ctype="text/plain; charset=utf-8")
            except FileNotFoundError:
                self._send("", ctype="text/plain; charset=utf-8")
        else:
            self._send(page("not found", "<h1>404</h1>"), status=404)

    def do_POST(self):
        if urlsplit(self.path).path != "/add":
            self._send(page("not found", "<h1>404</h1>"), status=404)
            return
        length = int(self.headers.get("Content-Length", "0"))
        form = parse_qs(self.rfile.read(length).decode("utf-8"))
        url = (form.get("url") or [""])[0].strip()
        if not url:
            self._send(add_page("", "", None), status=400)
            return
        action, rec = add_or_merge(
            url,
            (form.get("title") or [""])[0].strip(),
            (form.get("folder") or [""])[0].strip().lstrip("#"),
            (form.get("notes") or [""])[0].strip(),
        )
        self._send(saved_page(action, rec))

    def log_message(self, fmt, *args):  # quieter logging
        pass


def reformat(path):
    """Print the file in canonical form to stdout (a 'fmt' for bookmarks files)."""
    with open(path, encoding="utf-8") as f:
        for line in f:
            rec = parse_line(line)
            if rec:
                print(format_line(rec))


def main():
    print("bm: serving %s on http://%s:%d" % (FILE, HOST, PORT))
    ThreadingHTTPServer((HOST, PORT), Handler).serve_forever()


if __name__ == "__main__":
    import sys
    if len(sys.argv) > 1 and sys.argv[1] in ("--reformat", "fmt"):
        reformat(sys.argv[2] if len(sys.argv) > 2 else FILE)
    else:
        main()
