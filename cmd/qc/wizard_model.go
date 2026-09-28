package main

import (
	"slices"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// wizardLayout is the room the page gives the form, read by the fields that
// fill it (the pickers).
type wizardLayout struct {
	width, height int
}

// wizardPhase is where the wizard is.
type wizardPhase int

const (
	// phaseForm asks the questions of the form (or of an edited section).
	phaseForm wizardPhase = iota
	// phaseReview shows every answer and the command before running.
	phaseReview
	// phaseDone: run was chosen.
	phaseDone
	// phaseAborted: the wizard was cancelled.
	phaseAborted
)

// Review buttons, in order.
const (
	buttonRun = iota
	buttonEdit
	buttonCancel
	buttonCount
)

// wizardModel is the full-screen wizard: a step indicator, the huh form of
// the current section (with a summary of the answers beside it on wide
// terminals), then a review of everything with the equivalent command.
type wizardModel struct {
	answers *wizardAnswers
	ctx     wizardContext
	theme   *huh.Theme
	// all are the pages of the whole wizard, for the step indicator: the
	// form may hold only the pages of an edited section.
	all  []wizardStep
	form wizardForm

	phase wizardPhase
	// reviewed is set once the review was reached: every section is
	// answered.
	reviewed bool
	// button is the focused review button; choosing is set while picking
	// the section to edit, chosen being the highlighted one.
	button   int
	choosing bool
	chosen   section
	// scroll is the first line of the review card shown; scrollable is set
	// when the card is taller than the page (see reviewCard).
	scroll     int
	scrollable bool

	width, height int
	help          help.Model
}

// newWizardModel starts the wizard on the first page.
func newWizardModel(
	answers *wizardAnswers,
	ctx wizardContext,
) *wizardModel {
	if ctx.layout == nil {
		ctx.layout = &wizardLayout{}
	}

	theme := wizardTheme(ctx.style)
	h := help.New()
	h.Styles = theme.Help
	h.ShortSeparator = "  "

	m := &wizardModel{
		answers: answers, ctx: ctx, theme: theme,
		all:   answers.formSteps(ctx),
		form:  newWizardForm(answers.formSteps(ctx), theme),
		width: defaultPageWidth, height: defaultPageHeight,
		help: h,
	}
	m.resize(m.width, m.height)

	return m
}

// Init implements tea.Model.
func (m *wizardModel) Init() tea.Cmd {
	return m.form.form.Init()
}

// Update implements tea.Model.
func (m *wizardModel) Update(
	msg tea.Msg,
) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)

		if m.phase != phaseForm {
			return m, nil
		}

		_, cmd := m.form.form.Update(m.formSize())

		return m, cmd
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.phase = phaseAborted

			return m, tea.Quit
		}
	}

	if m.phase == phaseReview {
		return m, m.updateReview(msg)
	}

	return m, m.updateForm(msg)
}

// resize lays the page out for a terminal of width × height.
func (m *wizardModel) resize(
	width, height int,
) {
	m.width, m.height = width, height
	m.ctx.layout.width, m.ctx.layout.height = m.mainWidth(), m.formHeight()
}

// formSize is the size message of the form: the room left by the page.
func (m *wizardModel) formSize() tea.WindowSizeMsg {
	return tea.WindowSizeMsg{Width: m.mainWidth(), Height: m.formHeight()}
}

// updateForm forwards msg to the form; esc goes back a page unless the
// focused field uses it (to clear a filter).
func (m *wizardModel) updateForm(
	msg tea.Msg,
) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "esc" && !usesEsc(m.form.form.KeyBinds()) {
		return m.form.form.PrevGroup()
	}

	_, cmd := m.form.form.Update(msg)

	if m.form.form.State == huh.StateCompleted {
		m.phase, m.reviewed, m.button, m.choosing, m.scroll = phaseReview, true, buttonRun, false, 0

		return nil
	}

	return cmd
}

// usesEsc reports whether an enabled binding of the focused field is esc.
func usesEsc(
	bindings []key.Binding,
) bool {
	return slices.ContainsFunc(bindings, func(b key.Binding) bool {
		return b.Enabled() && slices.Contains(b.Keys(), "esc")
	})
}

// reviewKeys are the keys of the review.
var reviewKeys = struct {
	press, left, right, run, edit, cancel, back, up, down, sections key.Binding
}{
	press:    key.NewBinding(key.WithKeys("enter", " "), key.WithHelp("enter", "confirm")),
	left:     key.NewBinding(key.WithKeys("left", "shift+tab", "h"), key.WithHelp("←→", "choose")),
	right:    key.NewBinding(key.WithKeys("right", "tab", "l")),
	run:      key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "run")),
	edit:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
	cancel:   key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "cancel")),
	back:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	up:       key.NewBinding(key.WithKeys("up", "k", "pgup"), key.WithHelp("↑↓", "scroll")),
	down:     key.NewBinding(key.WithKeys("down", "j", "pgdown")),
	sections: key.NewBinding(key.WithKeys("1", "2", "3", "4", "5"), key.WithHelp("1-5", "edit section")),
}

// updateReview handles the keys of the review.
func (m *wizardModel) updateReview(
	msg tea.Msg,
) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	if m.choosing {
		return m.updateChooser(k)
	}

	keys := reviewKeys

	switch {
	case key.Matches(k, keys.press):
		return m.press(m.button)
	case key.Matches(k, keys.left):
		m.button = (m.button + buttonCount - 1) % buttonCount
	case key.Matches(k, keys.right):
		m.button = (m.button + 1) % buttonCount
	case key.Matches(k, keys.run):
		return m.press(buttonRun)
	case key.Matches(k, keys.edit):
		return m.press(buttonEdit)
	case key.Matches(k, keys.cancel):
		return m.press(buttonCancel)
	case key.Matches(k, keys.back):
		return m.edit(sectionOutputs)
	case key.Matches(k, keys.up):
		m.scroll = max(0, m.scroll-1)
	case key.Matches(k, keys.down):
		m.scroll++
	case key.Matches(k, keys.sections):
		return m.editDigit(k.String())
	}

	return nil
}

// press activates a review button.
func (m *wizardModel) press(
	button int,
) tea.Cmd {
	switch button {
	case buttonRun:
		m.phase = phaseDone

		return tea.Quit
	case buttonCancel:
		m.phase = phaseAborted

		return tea.Quit
	}

	m.choosing, m.chosen = true, sectionSource

	return nil
}

// updateChooser picks the section to edit.
func (m *wizardModel) updateChooser(
	k tea.KeyMsg,
) tea.Cmd {
	editable := m.editable()
	i := max(0, slices.Index(editable, m.chosen))

	switch {
	case key.Matches(k, reviewKeys.press):
		return m.edit(m.chosen)
	case key.Matches(k, reviewKeys.left):
		m.chosen = editable[(i+len(editable)-1)%len(editable)]
	case key.Matches(k, reviewKeys.right):
		m.chosen = editable[(i+1)%len(editable)]
	case key.Matches(k, reviewKeys.back):
		m.choosing = false
	case key.Matches(k, reviewKeys.sections):
		return m.editDigit(k.String())
	}

	return nil
}

// editable are the sections with a page to show, in order.
func (m *wizardModel) editable() []section {
	visible := visibleSections(m.all)

	var editable []section

	for s := sectionSource; s < sectionReview; s++ {
		if visible[s] {
			editable = append(editable, s)
		}
	}

	return editable
}

// editDigit edits the section numbered digit, when it has a page to show.
func (m *wizardModel) editDigit(
	digit string,
) tea.Cmd {
	s := section(digit[0] - '1')
	if !slices.Contains(m.editable(), s) {
		return nil
	}

	return m.edit(s)
}

// edit asks the pages of section s again (see editSteps), then reviews.
func (m *wizardModel) edit(
	s section,
) tea.Cmd {
	m.form = newWizardForm(editSteps(m.answers.formSteps(m.ctx), s), m.theme)
	m.phase, m.choosing = phaseForm, false

	init := m.form.form.Init()
	_, size := m.form.form.Update(m.formSize())

	return tea.Batch(init, size)
}
