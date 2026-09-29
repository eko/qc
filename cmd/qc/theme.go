package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/eko/qc/internal/tui"
)

// Brand colours of the wizard, those of the logo: the light variants keep
// contrast on light terminals.
var (
	brandOrange = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#FF9F43"}
	brandInk    = lipgloss.Color("#1A1008")
	brandMuted  = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#8C8279"}
)

// The neutral tones of the wizard, warm like the brand: text, secondary text
// (descriptions, values), and hairlines (rules, pending steps, key hints).
var (
	toneText   = lipgloss.AdaptiveColor{Light: "#1C1917", Dark: "#EDE8E3"}
	toneMuted  = lipgloss.AdaptiveColor{Light: "#6B625A", Dark: "#A39A91"}
	toneFaint  = lipgloss.AdaptiveColor{Light: "#A8A29E", Dark: "#5C554F"}
	toneGreen  = lipgloss.AdaptiveColor{Light: "#2E7D32", Dark: "#87D787"}
	toneRed    = lipgloss.AdaptiveColor{Light: "#C62828", Dark: "#FF6B6B"}
	toneHDR    = lipgloss.AdaptiveColor{Light: "#AD1457", Dark: "#FF87D7"}
	toneButton = lipgloss.AdaptiveColor{Light: "#E7E5E4", Dark: "#2E2A27"}
)

// glyphs are the symbols of the wizard: Unicode, or ASCII for terminals that
// cannot draw it (see newWizardStyle).
type glyphs struct {
	done, current, pending, skipped string
	sep, cursor, folder, check      string
	cross, dot, ellipsis, rail      string
	checked, unchecked, rule, arrow string
	border                          lipgloss.Border
}

var (
	unicodeGlyphs = glyphs{
		done: "✓", current: "●", pending: "○", skipped: "–",
		sep: "›", cursor: "❯", folder: "▸", check: "✓",
		cross: "✗", dot: "·", ellipsis: "…", rail: "━",
		checked: "■", unchecked: "□", rule: "─", arrow: "→",
		border: lipgloss.RoundedBorder(),
	}
	asciiGlyphs = glyphs{
		done: "+", current: "*", pending: "o", skipped: "-",
		sep: ">", cursor: ">", folder: "/", check: "+",
		cross: "x", dot: "-", ellipsis: "...", rail: "=",
		checked: "[x]", unchecked: "[ ]", rule: "-", arrow: "->",
		border: lipgloss.ASCIIBorder(),
	}
)

// wizardStyle is the look of the wizard: its glyphs and text styles.
type wizardStyle struct {
	g glyphs
	// plain is set without colours (NO_COLOR, no colour support): what
	// colours would tell (focus, selection) is told by glyphs and brackets.
	plain bool

	text, muted, faint, accent, bold, title, kicker lipgloss.Style
	success, danger, hdr                            lipgloss.Style
}

// newWizardStyle picks the glyphs and colours for the terminal: ASCII when
// NO_COLOR is set, TERM is dumb or the locale is not UTF-8.
func newWizardStyle(
	getenv func(string) string,
	profile termenv.Profile,
) wizardStyle {
	g := unicodeGlyphs
	if getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" || !utf8Locale(getenv) {
		g = asciiGlyphs
	}

	fg := func(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

	return wizardStyle{
		g:       g,
		plain:   profile == termenv.Ascii,
		text:    fg(toneText),
		muted:   fg(toneMuted),
		faint:   fg(toneFaint),
		accent:  fg(brandOrange),
		bold:    fg(toneText).Bold(true),
		title:   fg(brandOrange).Bold(true),
		kicker:  fg(toneMuted).Bold(true),
		success: fg(toneGreen),
		danger:  fg(toneRed),
		hdr:     fg(toneHDR).Bold(true),
	}
}

// utf8Locale reports whether the locale (LC_ALL, else LC_CTYPE, else LANG)
// is UTF-8. An unset locale counts as UTF-8, what terminals default to.
func utf8Locale(
	getenv func(string) string,
) bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := strings.ToLower(getenv(name)); v != "" {
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}

	return true
}

// bannerText is the tagline beside the bowl of the q in the wizard banner.
func bannerText() []string {
	title := lipgloss.NewStyle().Foreground(brandOrange).Bold(true).Render("qc · fast video quality analysis")
	subtitle := lipgloss.NewStyle().Foreground(brandMuted).Render("technical metrics · VMAF · per-title ladders")

	return []string{"", title, subtitle}
}

// wizardBanner is the pixel logo with its tagline, at rest.
func wizardBanner() string {
	return "\n" + tui.Logo(bannerText()...) + "\n"
}

// printBanner writes the banner to w, the logo lit by a passing glint when
// animate is set (see environment.animate).
func printBanner(
	w io.Writer,
	animate bool,
) error {
	if !animate {
		_, err := fmt.Fprintln(w, wizardBanner())

		return err //nolint:wrapcheck // a write to the terminal, reported as is
	}

	if _, err := io.WriteString(w, "\n"); err != nil {
		return err //nolint:wrapcheck // a write to the terminal, reported as is
	}

	if err := tui.AnimateLogo(w, bannerText()...); err != nil {
		return err
	}

	_, err := io.WriteString(w, "\n\n")

	return err //nolint:wrapcheck // a write to the terminal, reported as is
}

// wizardTheme styles huh's fields like the rest of the wizard: bold
// questions, muted help, the brand orange for what is focused or picked.
// The page frames the fields, so they have no border of their own.
func wizardTheme(
	s wizardStyle,
) *huh.Theme {
	t := huh.ThemeBase()
	focusedFieldStyles(&t.Focused, s)

	// Blurred fields (the other fields of a page) step back, without the
	// bar that marks the focused one.
	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.Title = s.muted.Bold(true)
	t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()
	t.Blurred.FocusedButton = t.Focused.BlurredButton

	t.Group.Title = s.title
	t.Group.Description = s.muted
	t.Help = helpStyles(s)

	return t
}

// focusedFieldStyles styles the focused field.
func focusedFieldStyles(
	f *huh.FieldStyles,
	s wizardStyle,
) {
	f.Base = lipgloss.NewStyle().Border(focusBar(s), false, false, false, true).BorderForeground(brandOrange).PaddingLeft(1)
	f.Card = f.Base
	f.Title = s.bold
	f.NoteTitle = s.bold
	f.Description = s.muted
	f.ErrorIndicator = s.danger.SetString(" " + s.g.cross)
	f.ErrorMessage = s.danger.SetString(s.g.cross)
	f.SelectSelector = s.accent.SetString(s.g.cursor + " ")
	f.MultiSelectSelector = s.accent.SetString(s.g.cursor + " ")
	f.NextIndicator = s.accent.MarginLeft(1).SetString(s.g.arrow)
	f.PrevIndicator = s.accent.MarginRight(1).SetString("<")
	f.Option = s.text
	f.SelectedOption = s.accent
	f.SelectedPrefix = s.accent.SetString(s.g.checked + " ")
	f.UnselectedOption = s.text
	f.UnselectedPrefix = s.faint.SetString(s.g.unchecked + " ")
	f.Directory = s.accent
	f.File = s.text
	f.TextInput.Cursor = s.accent
	f.TextInput.Placeholder = s.faint
	f.TextInput.Prompt = s.accent
	f.TextInput.Text = s.text
	f.FocusedButton, f.BlurredButton = buttonStyles(s)
	f.Next = f.FocusedButton
}

// focusBar is the bar left of the focused field.
func focusBar(
	s wizardStyle,
) lipgloss.Border {
	if s.g.rail == asciiGlyphs.rail {
		return lipgloss.Border{Left: "|"}
	}

	return lipgloss.Border{Left: "┃"}
}

// buttonPadding is the horizontal padding of a button label.
const buttonPadding = 2

// buttonStyles are the focused and blurred buttons: the brand fill, or
// brackets around the focused one without colours.
func buttonStyles(
	s wizardStyle,
) (focused, blurred lipgloss.Style) {
	if s.plain {
		brackets := lipgloss.Border{Left: "[", Right: "]"}

		return lipgloss.NewStyle().Border(brackets, false, true).Padding(0, 1).MarginRight(1),
			lipgloss.NewStyle().Padding(0, buttonPadding).MarginRight(1)
	}

	button := lipgloss.NewStyle().Padding(0, buttonPadding).MarginRight(1)

	return button.Foreground(brandInk).Background(brandOrange).Bold(true),
		button.Foreground(toneMuted).Background(toneButton)
}

// helpStyles are the key hints: keys in the text colour, what they do
// muted, faint separators.
func helpStyles(
	s wizardStyle,
) help.Styles {
	styles := help.New().Styles
	styles.ShortKey = s.muted.Bold(true)
	styles.ShortDesc = s.faint
	styles.ShortSeparator = s.faint
	styles.Ellipsis = s.faint
	styles.FullKey = styles.ShortKey
	styles.FullDesc = styles.ShortDesc
	styles.FullSeparator = styles.ShortSeparator

	return styles
}
