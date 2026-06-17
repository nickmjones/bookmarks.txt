package main

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sendKey(m model, k string) model {
	var msg tea.KeyMsg
	switch k {
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	nm, _ := m.Update(msg)
	return nm.(model)
}

func TestComputeFoldersAndFilter(t *testing.T) {
	books := []*Bookmark{
		{URL: "https://a", Folder: "x", Added: "2026-01-01"},
		{URL: "https://b", Folder: "y", Added: "2026-01-02"},
		{URL: "https://c", Added: "2026-01-03"}, // no folder
	}
	folders, counts := computeFolders(books)
	want := []string{folderAll, "x", "y", folderNone}
	if len(folders) != len(want) {
		t.Fatalf("folders = %v, want %v", folders, want)
	}
	for i := range want {
		if folders[i] != want[i] {
			t.Fatalf("folders = %v, want %v", folders, want)
		}
	}
	if counts[folderAll] != 3 || counts["x"] != 1 || counts[folderNone] != 1 {
		t.Fatalf("counts = %v", counts)
	}
	if n := len(itemsFor(books, "x")); n != 1 {
		t.Fatalf("folder x items = %d, want 1", n)
	}
	if n := len(itemsFor(books, folderNone)); n != 1 {
		t.Fatalf("(none) items = %d, want 1", n)
	}
	if n := len(itemsFor(books, folderAll)); n != 3 {
		t.Fatalf("All items = %d, want 3", n)
	}
}

func TestFocusAndFolderNav(t *testing.T) {
	books := []*Bookmark{
		{URL: "https://a", Folder: "x", Added: "1"},
		{URL: "https://b", Folder: "y", Added: "2"},
	}
	m := newModel(t.TempDir()+"/b.txt", books)
	if m.focus != focusList {
		t.Fatal("should start focused on the list")
	}
	m = sendKey(m, "tab") // -> sidebar
	if m.focus != focusSidebar {
		t.Fatal("tab did not focus the sidebar")
	}
	m = sendKey(m, "j") // All -> x
	if m.folders[m.folderSel] != "x" {
		t.Fatalf("expected folder x, got %q", m.folders[m.folderSel])
	}
	if n := len(m.list.Items()); n != 1 {
		t.Fatalf("list should be filtered to 1 item, got %d", n)
	}
	m = sendKey(m, "l") // -> list
	if m.focus != focusList {
		t.Fatal("l did not return focus to the list")
	}
	m = sendKey(m, "h") // -> sidebar again
	if m.focus != focusSidebar {
		t.Fatal("h did not focus the sidebar")
	}
}

func TestTwoPaneRenders(t *testing.T) {
	books := []*Bookmark{{URL: "https://a", Title: "A", Folder: "work", Added: "2026-01-01"}}
	m := newModel(t.TempDir()+"/b.txt", books)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := nm.(model).View()
	if !strings.Contains(out, "folders") {
		t.Fatal("sidebar header missing from view")
	}
	if !strings.Contains(out, "work") {
		t.Fatal("folder name missing from sidebar")
	}
}

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
