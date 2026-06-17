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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic
}

// resolveFile picks the bookmarks file: $BM_FILE, else a project-local
// ./bookmarks.txt when present, else the per-user default. Keep in sync with
// bm.py so the service and the TUI agree on where the file lives.
func resolveFile() string {
	if p := os.Getenv("BM_FILE"); p != "" {
		return p
	}
	if _, err := os.Stat("bookmarks.txt"); err == nil {
		return "bookmarks.txt" // convenience when run inside the project
	}
	return defaultFile()
}

// defaultFile is $XDG_CONFIG_HOME/bmtui/bookmarks.txt (≈ ~/.config/bmtui/…).
func defaultFile() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "bookmarks.txt"
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "bmtui", "bookmarks.txt")
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
	adding
)

type model struct {
	path  string
	books []*Bookmark // file order (preserved on save)
	list  list.Model
	state state
	w, h  int

	pal    palette   // the floating add/edit palette
	target *Bookmark // the bookmark being edited or deleted
	err    string

	lastMod  time.Time // for detecting external writes (e.g. the bookmarklet)
	lastSize int64

	focus     int      // focusList or focusSidebar
	folders   []string // sidebar entries (folderAll, names…, folderNone)
	counts    map[string]int
	folderSel int
}

// focus identifies which pane has keyboard focus.
const (
	focusList = iota
	focusSidebar
)

// Sentinel sidebar keys (real folder names can't contain a NUL byte).
const (
	folderAll  = "\x00all"
	folderNone = "\x00none"
)

const sidebarInner = 20 // sidebar content width

var (
	activeBorderColor = lipgloss.Color("170")
	dimBorderColor    = lipgloss.Color("240")

	confirmStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("9")).Padding(1, 2)
	sidebarBox       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	sidebarHeadStyle = lipgloss.NewStyle().Bold(true)
	sidebarSelStyle  = lipgloss.NewStyle().Bold(true).Foreground(activeBorderColor)

	paletteBox        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(activeBorderColor).Padding(1, 2)
	paletteTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(activeBorderColor)
	paletteLabelStyle = lipgloss.NewStyle().Faint(true)
	paletteFocusStyle = lipgloss.NewStyle().Bold(true).Foreground(activeBorderColor)
	paletteHelpStyle  = lipgloss.NewStyle().Foreground(dimBorderColor)
)

func computeFolders(books []*Bookmark) ([]string, map[string]int) {
	counts := map[string]int{}
	set := map[string]bool{}
	hasNone := false
	for _, b := range books {
		counts[folderAll]++
		if b.Folder == "" {
			hasNone = true
			counts[folderNone]++
		} else {
			set[b.Folder] = true
			counts[b.Folder]++
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	folders := append([]string{folderAll}, names...)
	if hasNone {
		folders = append(folders, folderNone)
	}
	return folders, counts
}

func folderLabel(key string) string {
	switch key {
	case folderAll:
		return "All"
	case folderNone:
		return "(none)"
	default:
		return key
	}
}

func folderMatches(b *Bookmark, key string) bool {
	switch key {
	case folderAll:
		return true
	case folderNone:
		return b.Folder == ""
	default:
		return b.Folder == key
	}
}

func itemsFor(books []*Bookmark, folderKey string) []list.Item {
	var sel []*Bookmark
	for _, b := range books {
		if folderMatches(b, folderKey) {
			sel = append(sel, b)
		}
	}
	return sortedItems(sel)
}

// trackingParams are query keys dropped during normalization. Keep in sync
// with TRACKING_PARAMS in bm.py.
var trackingParams = map[string]bool{
	"utm_source": true, "utm_medium": true, "utm_campaign": true,
	"utm_term": true, "utm_content": true, "fbclid": true, "gclid": true,
	"dclid": true, "msclkid": true, "mc_cid": true, "mc_eid": true,
	"ref": true, "ref_src": true, "ref_url": true, "igshid": true,
	"_hsenc": true, "_hsmi": true,
}

// normalizeURL is the canonical dedup key. Port of normalize_url in bm.py;
// the two must agree so the service and the TUI dedupe the same way.
func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	host := strings.ToLower(u.Hostname())
	netloc := host
	if p := u.Port(); p != "" && !((scheme == "http" && p == "80") || (scheme == "https" && p == "443")) {
		netloc = host + ":" + p
	}
	path := u.Path
	if path == "" {
		path = "/" // treat bare host and host/ as the same bookmark
	} else if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimRight(path, "/")
	}
	var kept []string
	if u.RawQuery != "" {
		for _, pair := range strings.Split(u.RawQuery, "&") {
			if pair == "" {
				continue
			}
			key := pair
			if i := strings.IndexByte(pair, '='); i >= 0 {
				key = pair[:i]
			}
			if trackingParams[strings.ToLower(key)] {
				continue
			}
			kept = append(kept, pair)
		}
	}
	out := scheme + "://" + netloc + path
	if len(kept) > 0 {
		out += "?" + strings.Join(kept, "&")
	}
	return out // fragment dropped
}

func today() string { return time.Now().Format("2006-01-02") }

func truncate(s string, n int) string {
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func newModel(path string, books []*Bookmark) model {
	folders, counts := computeFolders(books)
	d := list.NewDefaultDelegate()
	l := list.New(itemsFor(books, folderAll), d, 0, 0)
	l.Title = "bookmarks"
	l.SetStatusBarItemName("bookmark", "bookmarks")
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab/h", "folders")),
			key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "add from clipboard")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
			key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
			key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		}
	}
	m := model{path: path, books: books, list: l, folders: folders, counts: counts}
	m.stampMod()
	return m
}

func (m *model) toggleFocus() {
	if m.focus == focusList {
		m.focus = focusSidebar
	} else {
		m.focus = focusList
	}
}

// applyFolder reloads the bookmark pane for the currently selected folder.
func (m *model) applyFolder() {
	m.list.ResetFilter()
	m.list.SetItems(itemsFor(m.books, m.folders[m.folderSel]))
	m.list.Select(0)
}

func (m *model) moveFolder(delta int) {
	n := len(m.folders)
	if n == 0 {
		return
	}
	m.folderSel += delta
	if m.folderSel < 0 {
		m.folderSel = 0
	}
	if m.folderSel >= n {
		m.folderSel = n - 1
	}
	m.applyFolder()
}

// layout sizes the bookmark pane to the space left of the sidebar.
func (m *model) layout() {
	listW := m.w - (sidebarInner + 2) - 1 // sidebar border + a gap
	if listW < 10 {
		listW = 10
	}
	h := m.h
	if h < 1 {
		h = 1
	}
	m.list.SetSize(listW, h)
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd { return tickCmd() }

// refresh rebuilds folders and the list, keeping the selected folder and the
// cursor on the same bookmark if they survive the change.
func (m *model) refresh() {
	var selURL string
	if it, ok := m.list.SelectedItem().(item); ok {
		selURL = it.b.URL
	}
	selFolder := folderAll
	if m.folderSel < len(m.folders) {
		selFolder = m.folders[m.folderSel]
	}

	m.folders, m.counts = computeFolders(m.books)
	m.folderSel = 0 // default to "All" if the old folder vanished
	for i, f := range m.folders {
		if f == selFolder {
			m.folderSel = i
			break
		}
	}

	m.list.SetItems(itemsFor(m.books, m.folders[m.folderSel]))
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

// ---------------------------------------------------------------------------
// Floating add/edit palette
// ---------------------------------------------------------------------------

type paletteField struct{ label, value, placeholder string }

type palette struct {
	title  string
	labels []string
	inputs []textinput.Model
	focus  int
}

func newPalette(title string, fields []paletteField) palette {
	p := palette{title: title}
	for _, f := range fields {
		ti := textinput.New()
		ti.Prompt = ""
		ti.SetValue(f.value)
		ti.Placeholder = f.placeholder
		ti.Width = 44
		p.labels = append(p.labels, f.label)
		p.inputs = append(p.inputs, ti)
	}
	if len(p.inputs) > 0 {
		p.inputs[0].Focus()
	}
	return p
}

// move changes the focused field, wrapping around.
func (p *palette) move(d int) {
	n := len(p.inputs)
	if n == 0 {
		return
	}
	p.inputs[p.focus].Blur()
	p.focus = (p.focus + d + n) % n
	p.inputs[p.focus].Focus()
}

func (p *palette) update(msg tea.Msg) tea.Cmd {
	if len(p.inputs) == 0 {
		return nil
	}
	var cmd tea.Cmd
	p.inputs[p.focus], cmd = p.inputs[p.focus].Update(msg)
	return cmd
}

func (p palette) value(i int) string { return p.inputs[i].Value() }

func (p palette) view() string {
	labelW := 0
	for _, l := range p.labels {
		if len(l) > labelW {
			labelW = len(l)
		}
	}
	var b strings.Builder
	b.WriteString(paletteTitleStyle.Render(p.title))
	b.WriteString("\n\n")
	for i := range p.inputs {
		marker := "  "
		if i == p.focus {
			marker = paletteFocusStyle.Render("› ")
		}
		lbl := paletteLabelStyle.Render(fmt.Sprintf("%-*s", labelW, p.labels[i]))
		b.WriteString(marker + lbl + "  " + p.inputs[i].View() + "\n")
	}
	b.WriteString("\n")
	b.WriteString(paletteHelpStyle.Render("enter save · tab/⇧tab move · esc cancel"))
	return paletteBox.Render(b.String())
}

func cleanFolder(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
}

// overlayCenter composites fg centered over bg. Both may contain ANSI styling;
// ansi.Truncate/TruncateLeft slice the background by display cell, not byte.
func overlayCenter(bg, fg string, w, h int) string {
	bgLines := strings.Split(bg, "\n")
	for len(bgLines) < h {
		bgLines = append(bgLines, "")
	}
	fgLines := strings.Split(fg, "\n")
	fgW := 0
	for _, l := range fgLines {
		if x := ansi.StringWidth(l); x > fgW {
			fgW = x
		}
	}
	x := (w - fgW) / 2
	if x < 0 {
		x = 0
	}
	y := (h - len(fgLines)) / 2
	if y < 0 {
		y = 0
	}
	for i, fl := range fgLines {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		bl := bgLines[row]
		left := ansi.Truncate(bl, x, "")
		if pad := x - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(bl, x+ansi.StringWidth(fl), "")
		bgLines[row] = left + fl + right
	}
	return strings.Join(bgLines, "\n")
}

// addOrMerge appends a new bookmark, or merges into an existing one with the
// same normalized URL. Mirrors add_or_merge in bm.py.
func (m *model) addOrMerge(url, title, folder, notes string) {
	norm := normalizeURL(url)
	for _, b := range m.books {
		if normalizeURL(b.URL) == norm {
			if title != "" && b.Title == "" {
				b.Title = title
			}
			if folder != "" {
				b.Folder = folder
			}
			if notes != "" {
				b.Notes = notes
			}
			return
		}
	}
	m.books = append(m.books, &Bookmark{
		URL: url, Title: title, Folder: folder, Notes: notes, Added: today(),
	})
}

func (m *model) selectURL(url string) {
	for i, li := range m.list.Items() {
		if it, ok := li.(item); ok && it.b.URL == url {
			m.list.Select(i)
			return
		}
	}
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
	case adding:
		return m.updateAdding(msg)
	case confirming:
		return m.updateConfirming(msg)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
	case tea.KeyMsg:
		// While the bookmark filter is open, let it consume everything.
		if m.focus == focusList && m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			m.toggleFocus()
			return m, nil
		case "ctrl+k":
			clip, err := clipboard.ReadAll()
			if err != nil {
				m.err = "clipboard: " + err.Error()
				return m, nil
			}
			m.err = ""
			defFolder := ""
			if f := m.folders[m.folderSel]; f != folderAll && f != folderNone {
				defFolder = f
			}
			m.pal = newPalette("Add bookmark", []paletteField{
				{"URL", strings.TrimSpace(clip), "https://…"},
				{"Title", "", "title"},
				{"Folder", defFolder, "folder"},
				{"Notes", "", "why this matters"},
			})
			m.state = adding
			return m, textinput.Blink
		}

		if m.focus == focusSidebar {
			switch msg.String() {
			case "j", "down":
				m.moveFolder(1)
			case "k", "up":
				m.moveFolder(-1)
			case "g", "home":
				m.folderSel = 0
				m.applyFolder()
			case "G", "end":
				m.folderSel = len(m.folders) - 1
				m.applyFolder()
			case "l", "right", "enter":
				m.focus = focusList
			}
			return m, nil // the sidebar consumes all other keys
		}

		// focus == focusList
		switch msg.String() {
		case "h", "left":
			m.focus = focusSidebar
			return m, nil
		case "enter":
			if it, ok := m.list.SelectedItem().(item); ok {
				return m, openURL(it.b.URL)
			}
			return m, nil
		case "e":
			if it, ok := m.list.SelectedItem().(item); ok {
				m.target = it.b
				m.pal = newPalette("Edit bookmark", []paletteField{
					{"Title", it.b.Title, "title"},
					{"Folder", it.b.Folder, "folder"},
					{"Notes", it.b.Notes, "notes"},
				})
				m.state = editing
				return m, textinput.Blink
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
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) updateEditing(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			m.state = browsing
			return m, nil
		case "tab", "down":
			m.pal.move(1)
			return m, nil
		case "shift+tab", "up":
			m.pal.move(-1)
			return m, nil
		case "enter":
			m.target.Title = strings.TrimSpace(m.pal.value(0))
			m.target.Folder = cleanFolder(m.pal.value(1))
			m.target.Notes = strings.TrimSpace(m.pal.value(2))
			m.persist()
			m.refresh()
			m.state = browsing
			return m, nil
		}
	}
	cmd := m.pal.update(msg)
	return m, cmd
}

func (m model) updateAdding(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			m.state = browsing
			return m, nil
		case "tab", "down":
			m.pal.move(1)
			return m, nil
		case "shift+tab", "up":
			m.pal.move(-1)
			return m, nil
		case "enter":
			u := strings.TrimSpace(m.pal.value(0))
			if u != "" {
				folder := cleanFolder(m.pal.value(2))
				m.addOrMerge(u, strings.TrimSpace(m.pal.value(1)), folder, strings.TrimSpace(m.pal.value(3)))
				m.persist()
				m.refresh()
				// Jump to the new/updated bookmark in its folder so it's visible.
				target := folderAll
				if folder != "" {
					target = folder
				}
				for i, f := range m.folders {
					if f == target {
						m.folderSel = i
						break
					}
				}
				m.applyFolder()
				m.selectURL(u)
			}
			m.state = browsing
			return m, nil
		}
	}
	cmd := m.pal.update(msg)
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

func (m model) browseView() string {
	view := lipgloss.JoinHorizontal(lipgloss.Top, m.sidebarView(), m.list.View())
	if m.err != "" {
		view += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("error: "+m.err)
	}
	return view
}

func (m model) View() string {
	switch m.state {
	case editing, adding:
		return overlayCenter(m.browseView(), m.pal.view(), m.w, m.h)
	case confirming:
		label := m.target.Title
		if label == "" {
			label = m.target.URL
		}
		box := confirmStyle.Render(fmt.Sprintf(
			"Delete this bookmark?\n\n  %s\n  %s\n\n  (y) yes    (n) no",
			label, m.target.URL))
		return overlayCenter(m.browseView(), box, m.w, m.h)
	}
	return m.browseView()
}

func (m model) sidebarView() string {
	var b strings.Builder
	b.WriteString(sidebarHeadStyle.Render("folders"))
	b.WriteString("\n\n")
	for i, f := range m.folders {
		marker := "  "
		if i == m.folderSel {
			marker = "› "
		}
		label := folderLabel(f)
		count := fmt.Sprintf("%d", m.counts[f])
		avail := sidebarInner - lipgloss.Width(marker) - lipgloss.Width(count) - 1
		label = truncate(label, avail)
		gap := sidebarInner - lipgloss.Width(marker) - lipgloss.Width(label) - lipgloss.Width(count)
		if gap < 1 {
			gap = 1
		}
		row := marker + label + strings.Repeat(" ", gap) + count
		if i == m.folderSel {
			row = sidebarSelStyle.Render(row)
		}
		b.WriteString(row + "\n")
	}

	h := m.h - 2 // account for the border
	if h < 1 {
		h = 1
	}
	box := sidebarBox.Width(sidebarInner).Height(h)
	if m.focus == focusSidebar {
		box = box.BorderForeground(activeBorderColor)
	} else {
		box = box.BorderForeground(dimBorderColor)
	}
	return box.Render(b.String())
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
