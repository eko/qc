package main

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
)

// pickerEntry is a line of the file list: a folder or a video file.
type pickerEntry struct {
	name string
	dir  bool
	size int64
}

// parentEntry is the line that goes up a folder.
const parentEntry = ".."

// Messages of the pickers. Every field of a page receives them: each picker
// keeps its own.
type (
	// previewDueMsg probes the highlighted file once the highlight settled.
	previewDueMsg struct {
		picker *videoPicker
		seq    int
	}
	// probedMsg tells that a file was probed (its summary is cached).
	probedMsg struct{ path string }
)

// videoPicker is a huh field that browses folders for a video: type to
// filter, enter to open a folder or pick a file, the highlighted file
// described by a cheap ffprobe. A file ffprobe cannot read, or without a
// video stream, cannot be picked.
type videoPicker struct {
	title, description string
	value              *string
	cache              *videoCache
	layout             *wizardLayout
	style              wizardStyle
	// startDir is where to browse first when nothing is picked yet (the
	// folder of the source, for the reference); "" is the working directory.
	startDir func() string
	// more, when set, lets the picker take several videos (see Several);
	// marked are those selected, marking is set while the one being added is
	// probed.
	more    *[]string
	marked  []string
	marking bool

	cwd, dir string
	entries  []pickerEntry
	// visible are the entries matching the filter, cursor indexes them.
	visible  []pickerEntry
	cursor   int
	offset   int
	filter   textinput.Model
	opened   bool
	focused  bool
	err      error
	listErr  error
	pending  string
	seq      int
	width    int
	position huh.FieldPosition
}

// newVideoPicker returns a picker writing the chosen path to value.
func newVideoPicker(
	title, description string,
	value *string,
	ctx wizardContext,
) *videoPicker {
	filter := textinput.New()
	filter.Prompt = ""
	filter.Placeholder = "type to filter"
	filter.PromptStyle = ctx.style.accent
	filter.PlaceholderStyle = ctx.style.faint
	filter.TextStyle = ctx.style.text
	filter.Cursor.Style = ctx.style.accent

	cwd, _ := os.Getwd()

	return &videoPicker{
		title: title, description: description, value: value,
		cache: ctx.videos, layout: ctx.layout, style: ctx.style,
		cwd: cwd, filter: filter, width: defaultPageWidth,
	}
}

// StartIn sets the folder browsed first when nothing is picked yet.
func (f *videoPicker) StartIn(
	dir func() string,
) *videoPicker {
	f.startDir = dir

	return f
}

// open lists the folder of the picked file, else the start folder.
func (f *videoPicker) open() {
	f.opened = true
	f.restore()

	dir, name := f.cwd, ""

	switch {
	case *f.value != "":
		abs := f.absolute(*f.value)
		dir, name = filepath.Dir(abs), filepath.Base(abs)
	case f.startDir != nil && f.startDir() != "":
		dir = f.absolute(filepath.Dir(f.startDir()))
	}

	f.chdir(dir)
	f.moveTo(name)
}

// absolute resolves path against the working directory.
func (f *videoPicker) absolute(
	path string,
) string {
	if filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(f.cwd, path)
}

// relative is the path the command shows: relative to the working
// directory when below it, absolute otherwise.
func (f *videoPicker) relative(
	path string,
) string {
	if rel, err := filepath.Rel(f.cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}

	return path
}

// chdir lists dir, keeping the previous listing when it cannot be read.
func (f *videoPicker) chdir(
	dir string,
) {
	entries, err := listVideos(dir)
	if err != nil {
		f.listErr = err

		return
	}

	f.dir, f.entries, f.listErr = dir, entries, nil
	f.filter.SetValue("")
	f.refilter()
}

// listVideos lists the folders and the video files of dir, folders first,
// hidden entries left out.
func listVideos(
	dir string,
) ([]pickerEntry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}

	var entries []pickerEntry

	for _, item := range items {
		name := item.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}

		info, err := os.Stat(filepath.Join(dir, name)) // follows links
		if err != nil {
			continue
		}

		switch {
		case info.IsDir():
			entries = append(entries, pickerEntry{name: name, dir: true})
		case isVideoName(name):
			entries = append(entries, pickerEntry{name: name, size: info.Size()})
		}
	}

	slices.SortStableFunc(entries, func(a, b pickerEntry) int {
		if a.dir != b.dir {
			return map[bool]int{true: -1, false: 1}[a.dir]
		}

		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})

	return entries, nil
}

// isVideoName reports whether name has a video extension.
func isVideoName(
	name string,
) bool {
	return slices.Contains(videoExtensions, strings.ToLower(filepath.Ext(name)))
}

// refilter keeps the entries matching the filter, the parent folder first
// while nothing is typed.
func (f *videoPicker) refilter() {
	query := strings.ToLower(strings.TrimSpace(f.filter.Value()))
	f.visible = f.visible[:0]

	if query == "" && filepath.Dir(f.dir) != f.dir {
		f.visible = append(f.visible, pickerEntry{name: parentEntry, dir: true})
	}

	for _, e := range f.entries {
		if strings.Contains(strings.ToLower(e.name), query) {
			f.visible = append(f.visible, e)
		}
	}

	f.cursor, f.offset = 0, 0
	f.skipParent()
}

// skipParent starts the cursor on the first real entry.
func (f *videoPicker) skipParent() {
	if len(f.visible) > 1 && f.visible[0].name == parentEntry {
		f.cursor = 1
	}
}

// moveTo puts the cursor on the entry called name, if listed.
func (f *videoPicker) moveTo(
	name string,
) {
	if i := slices.IndexFunc(f.visible, func(e pickerEntry) bool { return e.name == name }); i >= 0 {
		f.cursor = i
	}
}

// current is the highlighted entry.
func (f *videoPicker) current() (pickerEntry, bool) {
	if f.cursor < 0 || f.cursor >= len(f.visible) {
		return pickerEntry{}, false
	}

	return f.visible[f.cursor], true
}

// currentPath is the path of the highlighted entry, "" when none.
func (f *videoPicker) currentPath() string {
	e, ok := f.current()
	if !ok {
		return ""
	}

	return filepath.Join(f.dir, e.name)
}

// Focus implements huh.Field: lists the start folder on first focus.
func (f *videoPicker) Focus() tea.Cmd {
	f.focused = true
	if !f.opened {
		f.open()
	}

	return tea.Batch(f.filter.Focus(), f.highlighted())
}

// Blur implements huh.Field.
func (f *videoPicker) Blur() tea.Cmd {
	f.focused = false
	f.filter.Blur()

	return nil
}

// highlighted schedules the preview of the highlighted file, unless it is
// known already.
func (f *videoPicker) highlighted() tea.Cmd {
	e, ok := f.current()
	if !ok || e.dir {
		return nil
	}

	if _, known := f.cache.cached(f.currentPath()); known {
		return nil
	}

	f.seq++
	seq := f.seq

	return tea.Tick(previewDelay, func(time.Time) tea.Msg { return previewDueMsg{picker: f, seq: seq} })
}

// probe probes path in the background.
func (f *videoPicker) probe(
	path string,
) tea.Cmd {
	return func() tea.Msg {
		f.cache.inspect(path)

		return probedMsg{path: path}
	}
}

// Update implements huh.Field.
func (f *videoPicker) Update(
	msg tea.Msg,
) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case previewDueMsg:
		if m.picker == f && m.seq == f.seq {
			return f, f.probe(f.currentPath())
		}
	case probedMsg:
		if m.path == f.pending && f.pending != "" {
			f.pending = ""

			if f.marking {
				f.marking = false
				f.mark(m.path)

				return f, nil
			}

			return f, f.pick(m.path)
		}
	case tea.KeyMsg:
		if f.focused {
			return f, f.handleKey(m)
		}
	}

	return f, nil
}

// pickerKeys are the keys of the pickers.
var pickerKeys = struct {
	up, down, pageUp, pageDown, top, bottom key.Binding
	open, parent, back, clear, next, prev   key.Binding
	mark                                    key.Binding
}{
	up:       key.NewBinding(key.WithKeys("up", "ctrl+p", "ctrl+k"), key.WithHelp("↑↓", "move")),
	down:     key.NewBinding(key.WithKeys("down", "ctrl+n", "ctrl+j")),
	pageUp:   key.NewBinding(key.WithKeys("pgup")),
	pageDown: key.NewBinding(key.WithKeys("pgdown")),
	top:      key.NewBinding(key.WithKeys("home")),
	bottom:   key.NewBinding(key.WithKeys("end")),
	open:     key.NewBinding(key.WithKeys("enter", "right"), key.WithHelp("enter", "open/pick")),
	parent:   key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "up a folder")),
	back:     key.NewBinding(key.WithKeys("backspace")),
	clear:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
	next:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next")),
	prev:     key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "back")),
	mark:     key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select")),
}

// handleKey navigates, filters or picks.
func (f *videoPicker) handleKey(
	m tea.KeyMsg,
) tea.Cmd {
	f.err = nil
	k := pickerKeys

	switch {
	case key.Matches(m, k.up, k.down, k.pageUp, k.pageDown, k.top, k.bottom):
		f.move(m)

		return f.highlighted()
	case key.Matches(m, k.open):
		return f.activate(m.String() == "right")
	case f.more != nil && key.Matches(m, k.mark):
		return f.toggle()
	case key.Matches(m, k.parent), key.Matches(m, k.back) && f.filter.Value() == "":
		return f.up()
	case key.Matches(m, k.clear) && f.filter.Value() != "":
		f.filter.SetValue("")
		f.refilter()

		return f.highlighted()
	case key.Matches(m, k.next):
		return f.keep()
	case key.Matches(m, k.prev):
		return huh.PrevField
	}

	var cmd tea.Cmd

	before := f.filter.Value()
	f.filter, cmd = f.filter.Update(m)

	if f.filter.Value() != before {
		f.refilter()

		return tea.Batch(cmd, f.highlighted())
	}

	return cmd
}

// move moves the cursor.
func (f *videoPicker) move(
	m tea.KeyMsg,
) {
	page := max(1, f.rows()-1)
	last := len(f.visible) - 1

	switch {
	case key.Matches(m, pickerKeys.up):
		f.cursor--
	case key.Matches(m, pickerKeys.down):
		f.cursor++
	case key.Matches(m, pickerKeys.pageUp):
		f.cursor -= page
	case key.Matches(m, pickerKeys.pageDown):
		f.cursor += page
	case key.Matches(m, pickerKeys.top):
		f.cursor = 0
	case key.Matches(m, pickerKeys.bottom):
		f.cursor = last
	}

	f.cursor = min(max(f.cursor, 0), max(last, 0))
}

// activate opens the highlighted folder, or picks the highlighted file
// (with enter only).
func (f *videoPicker) activate(
	folderOnly bool,
) tea.Cmd {
	e, ok := f.current()
	if !ok {
		return nil
	}

	if e.dir {
		if e.name == parentEntry {
			return f.up()
		}

		f.chdir(filepath.Join(f.dir, e.name))

		return f.highlighted()
	}

	if folderOnly {
		return nil
	}

	if len(f.marked) > 0 {
		return f.confirm()
	}

	path := f.currentPath()
	if _, known := f.cache.cached(path); known {
		return f.pick(path)
	}

	f.pending = path

	return f.probe(path)
}

// up lists the parent folder, the cursor on the folder left.
func (f *videoPicker) up() tea.Cmd {
	left := filepath.Base(f.dir)
	f.chdir(filepath.Dir(f.dir))
	f.moveTo(left)

	return f.highlighted()
}

// pick chooses path when the run can use it, and moves on.
func (f *videoPicker) pick(
	path string,
) tea.Cmd {
	if err := f.cache.inspect(path).validate(); err != nil {
		f.err = err

		return nil
	}

	*f.value = f.relative(path)

	if f.more != nil {
		*f.more = nil
	}

	return huh.NextField
}

// keep moves on with the selected videos, else with the file picked
// before, if any.
func (f *videoPicker) keep() tea.Cmd {
	if len(f.marked) > 0 {
		return f.confirm()
	}

	if err := requirePath(*f.value); err != nil {
		f.err = err

		return nil
	}

	return huh.NextField
}

// rows is the number of file lines that fit the page.
func (f *videoPicker) rows() int {
	height := defaultPickerHeight
	if f.layout != nil && f.layout.height > 0 {
		height = f.layout.height
	}

	return max(minPickerRows, height-f.chromeHeight())
}

// Picker sizes: the height without a page (tests, accessible mode), the
// fewest file lines, and the preview column of wide pages.
const (
	defaultPickerHeight = 16
	minPickerRows       = 3
	previewWidth        = 34
	sideBySideWidth     = 88
)

// Error implements huh.Field.
func (f *videoPicker) Error() error { return f.err }

// Skip implements huh.Field.
func (*videoPicker) Skip() bool { return false }

// Zoom implements huh.Field: a focused picker takes the whole page.
func (f *videoPicker) Zoom() bool { return f.focused }

// KeyBinds implements huh.Field.
func (f *videoPicker) KeyBinds() []key.Binding {
	k := pickerKeys
	k.clear.SetEnabled(f.filter.Value() != "")
	k.next.SetEnabled(*f.value != "" || len(f.marked) > 0)
	k.prev.SetEnabled(!f.position.IsFirst())
	k.mark.SetEnabled(f.more != nil)

	if len(f.marked) > 0 {
		k.open.SetHelp("enter", "continue")
		k.mark.SetHelp("space", "add/remove")
	}

	return []key.Binding{k.up, k.open, k.mark, k.parent, k.clear, k.next, k.prev}
}

// Init implements huh.Field.
func (*videoPicker) Init() tea.Cmd { return nil }

// Run implements huh.Field.
func (f *videoPicker) Run() error {
	return huh.Run(f) //nolint:wrapcheck // huh's errors are the field's
}

// RunAccessible implements huh.Field: asks for a path until the run can use
// it (an empty answer keeps the file picked before).
func (f *videoPicker) RunAccessible(
	w io.Writer,
	r io.Reader,
) error {
	scanner := bufio.NewScanner(r)

	for {
		_, _ = fmt.Fprint(w, f.title+" (path): ")
		if !scanner.Scan() {
			_, _ = fmt.Fprintln(w)

			return io.ErrUnexpectedEOF
		}

		path := cmp.Or(strings.TrimSpace(scanner.Text()), *f.value)
		if err := f.check(path); err != nil {
			_, _ = fmt.Fprintln(w, err)

			continue
		}

		*f.value = path

		if f.more == nil {
			return nil
		}

		return f.askMore(w, scanner)
	}
}

// check validates a typed path.
func (f *videoPicker) check(
	path string,
) error {
	if err := requirePath(path); err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return errors.New("no such file")
	}

	if !isVideoName(path) {
		return errors.New("not a video file (" + strings.Join(videoExtensions, " ") + ")")
	}

	return f.cache.inspect(path).validate()
}

// WithTheme implements huh.Field: pickers follow the wizard style.
func (f *videoPicker) WithTheme(*huh.Theme) huh.Field { return f }

// WithAccessible implements huh.Field.
func (f *videoPicker) WithAccessible(bool) huh.Field { return f }

// WithKeyMap implements huh.Field: pickers have their own keys.
func (f *videoPicker) WithKeyMap(*huh.KeyMap) huh.Field { return f }

// WithWidth implements huh.Field.
func (f *videoPicker) WithWidth(
	width int,
) huh.Field {
	f.width = width

	return f
}

// WithHeight implements huh.Field: pickers fill the page (see rows).
func (f *videoPicker) WithHeight(int) huh.Field { return f }

// WithPosition implements huh.Field.
func (f *videoPicker) WithPosition(
	p huh.FieldPosition,
) huh.Field {
	f.position = p

	return f
}

// GetKey implements huh.Field.
func (*videoPicker) GetKey() string { return "" }

// GetValue implements huh.Field.
func (f *videoPicker) GetValue() any { return *f.value }

// truncate cuts s to width cells with an ellipsis.
func truncate(
	s string,
	width int,
	ellipsis string,
) string {
	return ansi.Truncate(s, max(width, 0), ellipsis)
}

// truncateLeft cuts s to width cells, keeping its end: the end of a path
// tells where it is.
func truncateLeft(
	s string,
	width int,
	ellipsis string,
) string {
	if ansi.StringWidth(s) <= width {
		return s
	}

	return ellipsis + ansi.TruncateLeft(s, ansi.StringWidth(s)-width+ansi.StringWidth(ellipsis), "")
}
