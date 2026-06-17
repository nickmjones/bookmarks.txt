package main

import (
	"os"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []string{
		`https://www.apple.com "Apple" #technology [makes computers and operating systems] added:2026-06-16`,
		`https://github.com/charmbracelet "Charmbracelet" [produces tools] added:2026-06-16`,
		`https://danluu.com/input-lag/`,
		`https://x.com "He said \"hi\"" #w [a note with \] bracket] added:2026-01-02`,
		`https://y.com "T" #f [n] added:2026-01-02 via:hn pub:2010 loosetoken`,
	}
	for _, in := range cases {
		got := formatLine(parseLine(in))
		if got != in {
			t.Errorf("round-trip mismatch:\n in:  %s\n out: %s", in, got)
		}
	}
}

func TestFields(t *testing.T) {
	b := parseLine(`https://www.apple.com "Apple" #technology [makes computers] added:2026-06-16`)
	if b.URL != "https://www.apple.com" || b.Title != "Apple" || b.Folder != "technology" ||
		b.Notes != "makes computers" || b.Added != "2026-06-16" {
		t.Errorf("bad parse: %+v", b)
	}
}

// Reserialize the real file so we can diff it against bm.py's output.
func TestReserializeRealFile(t *testing.T) {
	books, err := load("../bookmarks.txt")
	if err != nil {
		t.Skip("no real file")
	}
	var out string
	for _, b := range books {
		out += formatLine(b) + "\n"
	}
	if err := os.WriteFile("/tmp/go_out.txt", []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReloadDetectsExternalWrite(t *testing.T) {
	tmp := t.TempDir() + "/b.txt"
	os.WriteFile(tmp, []byte(`https://a.com "A" added:2026-01-01`+"\n"), 0o644)
	books, _ := load(tmp)
	m := newModel(tmp, books)
	if len(m.books) != 1 {
		t.Fatalf("expected 1 book, got %d", len(m.books))
	}
	// Simulate the bookmarklet/service appending a row while the TUI is open.
	f, _ := os.OpenFile(tmp, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`https://b.com "B" added:2026-01-02` + "\n")
	f.Close()
	m.reloadIfChanged()
	if len(m.books) != 2 {
		t.Fatalf("reload did not pick up the new row: got %d books", len(m.books))
	}
	// A subsequent reload with no change must be a no-op (no reload churn).
	before := m.lastMod
	m.reloadIfChanged()
	if !m.lastMod.Equal(before) {
		t.Fatalf("reloadIfChanged re-read an unchanged file")
	}
}
