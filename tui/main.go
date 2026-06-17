// bmtui — a Charm/Bubble Tea terminal UI for browsing and curating a
// bookmarks.txt file (see ../SPEC.md). It is the "manage/read" companion to the
// bm.py capture service; both operate on the same plain-text file.
//
// Keys: ↑/↓ or j/k move · / filter · enter open in browser · e edit · d delete · q quit
//
// File resolution: $BM_FILE, else ./bookmarks.txt, else ../bookmarks.txt.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Format parsing / serialization — a Go port of bm.py (keep them in sync).
//   URL  "Title"  #folder  [notes]  added:YYYY-MM-DD
// ---------------------------------------------------------------------------

type Bookmark struct {
	URL, Title, Folder, Notes, Added string
	Extra                            [][2]string // preserved unknown key:values
	Stray                            []string    // preserved unmodeled tokens
}

func readDelimited(s string, start int, closeC byte) (string, int) {
	var b strings.Builder
	for i := start + 1; i < len(s); {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		if c == closeC {
			return b.String(), i + 1
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), len(s) // unterminated: take the rest
}

func parseLine(line string) *Bookmark {
	s := strings.TrimSpace(line)
	if s == "" {
		return nil
	}
	head, rest := s, ""
	if idx := strings.IndexByte(s, ' '); idx >= 0 {
		head, rest = s[:idx], s[idx+1:]
	}
	b := &Bookmark{URL: head}
	for i, n := 0, len(rest); i < n; {
		c := rest[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '"':
			b.Title, i = readDelimited(rest, i, '"')
		case c == '[':
			b.Notes, i = readDelimited(rest, i, ']')
		case c == '#':
			j := i + 1
			for j < n && rest[j] != ' ' && rest[j] != '\t' {
				j++
			}
			b.Folder, i = rest[i+1:j], j
		default:
			j := i
			for j < n && rest[j] != ' ' && rest[j] != '\t' {
				j++
			}
			tok := rest[i:j]
			i = j
			if idx := strings.IndexByte(tok, ':'); idx > 0 {
				k, v := tok[:idx], tok[idx+1:]
				if k == "added" {
					b.Added = v
				} else {
					b.Extra = append(b.Extra, [2]string{k, v})
				}
			} else {
				b.Stray = append(b.Stray, tok)
			}
		}
	}
	return b
}

func esc(v string, closeC byte) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	return strings.ReplaceAll(v, string(closeC), "\\"+string(closeC))
}

func formatLine(b *Bookmark) string {
	parts := []string{b.URL}
	if b.Title != "" {
		parts = append(parts, "\""+esc(b.Title, '"')+"\"")
	}
	if b.Folder != "" {
		parts = append(parts, "#"+b.Folder)
	}
	if b.Notes != "" {
		parts = append(parts, "["+esc(b.Notes, ']')+"]")
	}
	if b.Added != "" {
		parts = append(parts, "added:"+b.Added)
	}
	for _, kv := range b.Extra {
		parts = append(parts, kv[0]+":"+kv[1])
	}
	parts = append(parts, b.Stray...)
	return strings.Join(parts, " ")
}

func load(path string) ([]*Bookmark, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var books []*Bookmark
	for _, line := range strings.Split(string(data), "\n") {
		if b := parseLine(line); b != nil {
			books = append(books, b)
		}
	}
	return books, nil
}

func save(path string, books []*Bookmark) error {
	var sb strings.Builder
	for _, b := range books {
		sb.WriteString(formatLine(b))
		sb.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic
}

func resolveFile() string {
	if p := os.Getenv("BM_FILE"); p != "" {
		return p
	}
	if _, err := os.Stat("bookmarks.txt"); err == nil {
		return "bookmarks.txt"
	}
	return filepath.Join("..", "bookmarks.txt")
}

// ---------------------------------------------------------------------------
// List item
// ---------------------------------------------------------------------------

type item struct{ b *Bookmark }

func (i item) Title() string {
	if i.b.Title != "" {
		return i.b.Title
	}
	return i.b.URL
}

func (i item) Description() string {
	var parts []string
	if i.b.Folder != "" {
		parts = append(parts, "#"+i.b.Folder)
	}
	if i.b.Added != "" {
		parts = append(parts, i.b.Added)
	}
	parts = append(parts, i.b.URL)
	return strings.Join(parts, " · ")
}

func (i item) FilterValue() string {
	return strings.Join([]string{i.b.Title, i.b.URL, i.b.Folder, i.b.Notes}, " ")
}

func sortedItems(books []*Bookmark) []list.Item {
	cp := make([]*Bookmark, len(books))
	copy(cp, books)
	sort.SliceStable(cp, func(i, j int) bool {
		if cp[i].Added != cp[j].Added {
			return cp[i].Added > cp[j].Added // newest first
		}
		return strings.ToLower(cp[i].Title) < strings.ToLower(cp[j].Title)
	})
	its := make([]list.Item, len(cp))
	for i, b := range cp {
		its[i] = item{b}
	}
	return its
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type state int

const (
	browsing state = iota
	editing
	confirming
)

type model struct {
	path  string
	books []*Bookmark // file order (preserved on save)
	list  list.Model
	state state
	w, h  int

	form    *huh.Form
	target  *Bookmark // the bookmark being edited or deleted
	fTitle  string
	fFolder string
	fNotes  string
	err     string

	lastMod  time.Time // for detecting external writes (e.g. the bookmarklet)
	lastSize int64
}

var confirmStyle = lipgloss.NewStyle().Padding(1, 2)

func newModel(path string, books []*Bookmark) model {
	d := list.NewDefaultDelegate()
	l := list.New(sortedItems(books), d, 0, 0)
	l.Title = "bookmarks"
	l.SetStatusBarItemName("bookmark", "bookmarks")
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
			key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
			key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		}
	}
	m := model{path: path, books: books, list: l}
	m.stampMod()
	return m
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd { return tickCmd() }

// refresh rebuilds the list, keeping the cursor on the same bookmark if it survives.
func (m *model) refresh() {
	var selURL string
	if it, ok := m.list.SelectedItem().(item); ok {
		selURL = it.b.URL
	}
	m.list.SetItems(sortedItems(m.books))
	if selURL != "" {
		for i, li := range m.list.Items() {
			if it, ok := li.(item); ok && it.b.URL == selURL {
				m.list.Select(i)
				break
			}
		}
	}
}

// stampMod records the file's current mtime/size so we can detect later writes.
func (m *model) stampMod() {
	if fi, err := os.Stat(m.path); err == nil {
		m.lastMod, m.lastSize = fi.ModTime(), fi.Size()
	}
}

// reloadIfChanged reloads the file when something else has written it.
func (m *model) reloadIfChanged() {
	fi, err := os.Stat(m.path)
	if err != nil {
		return
	}
	if fi.ModTime().Equal(m.lastMod) && fi.Size() == m.lastSize {
		return
	}
	books, err := load(m.path)
	if err != nil {
		return
	}
	m.books = books
	m.lastMod, m.lastSize = fi.ModTime(), fi.Size()
	m.refresh()
}

func (m *model) persist() {
	if err := save(m.path, m.books); err != nil {
		m.err = err.Error()
		return
	}
	m.stampMod() // our own write; don't let the next tick reload over it
}

func (m *model) newEditForm(b *Bookmark) *huh.Form {
	m.fTitle, m.fFolder, m.fNotes = b.Title, b.Folder, b.Notes
	f := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Title").Value(&m.fTitle),
		huh.NewInput().Title("Folder").Value(&m.fFolder),
		huh.NewText().Title("Notes").Value(&m.fNotes),
	))
	w := m.w
	if w > 72 || w == 0 {
		w = 72
	}
	return f.WithWidth(w)
}

func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		var c *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			c = exec.Command("open", url)
		case "windows":
			c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			c = exec.Command("xdg-open", url)
		}
		_ = c.Start()
		return nil
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tickMsg); ok {
		if m.state == browsing { // don't swap the list out mid-edit/confirm
			m.reloadIfChanged()
		}
		return m, tickCmd()
	}

	switch m.state {
	case editing:
		return m.updateEditing(msg)
	case confirming:
		return m.updateConfirming(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.list.SetSize(msg.Width, msg.Height)
	case tea.KeyMsg:
		if m.list.FilterState() != list.Filtering { // don't steal keys mid-filter
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "enter":
				if it, ok := m.list.SelectedItem().(item); ok {
					return m, openURL(it.b.URL)
				}
				return m, nil
			case "e":
				if it, ok := m.list.SelectedItem().(item); ok {
					m.target = it.b
					m.form = m.newEditForm(it.b)
					m.state = editing
					return m, m.form.Init()
				}
				return m, nil
			case "d":
				if it, ok := m.list.SelectedItem().(item); ok {
					m.target = it.b
					m.state = confirming
				}
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) updateEditing(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" {
		m.state = browsing
		m.form = nil
		return m, nil
	}
	fm, cmd := m.form.Update(msg)
	if f, ok := fm.(*huh.Form); ok {
		m.form = f
	}
	switch m.form.State {
	case huh.StateCompleted:
		m.target.Title = strings.TrimSpace(m.fTitle)
		m.target.Folder = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.fFolder), "#"))
		m.target.Notes = strings.TrimSpace(m.fNotes)
		m.persist()
		m.refresh()
		m.state = browsing
		m.form = nil
		return m, nil
	case huh.StateAborted:
		m.state = browsing
		m.form = nil
		return m, nil
	}
	return m, cmd
}

func (m model) updateConfirming(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "y", "Y":
			out := m.books[:0]
			for _, b := range m.books {
				if b != m.target {
					out = append(out, b)
				}
			}
			m.books = out
			m.persist()
			m.refresh()
			m.state = browsing
		case "n", "N", "esc", "q":
			m.state = browsing
		}
	}
	return m, nil
}

func (m model) View() string {
	switch m.state {
	case editing:
		return m.form.View()
	case confirming:
		label := m.target.Title
		if label == "" {
			label = m.target.URL
		}
		return confirmStyle.Render(fmt.Sprintf(
			"Delete this bookmark?\n\n  %s\n  %s\n\n  (y) yes    (n) no",
			label, m.target.URL))
	}
	if m.err != "" {
		return m.list.View() + "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("error: "+m.err)
	}
	return m.list.View()
}

// reformat reads a bookmarks file and prints it in canonical form to stdout.
// Used as a "fmt" for the format and to verify Go/Python serialization parity.
func reformat(args []string) {
	path := resolveFile()
	if len(args) > 0 {
		path = args[0]
	}
	books, err := load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bmtui:", err)
		os.Exit(1)
	}
	for _, b := range books {
		fmt.Println(formatLine(b))
	}
}

// version is set at build time via -ldflags by GoReleaser.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--reformat", "fmt":
			reformat(os.Args[2:])
			return
		case "--version", "-v":
			fmt.Println("bmtui", version)
			return
		}
	}
	path := resolveFile()
	books, err := load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bmtui:", err)
		os.Exit(1)
	}
	p := tea.NewProgram(newModel(path, books), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "bmtui:", err)
		os.Exit(1)
	}
}
