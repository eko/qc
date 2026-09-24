package main

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/internal/tui"
)

// Brand colours of the wizard, those of the logo: the light variants keep
// contrast on light terminals.
var (
	brandOrange = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#FF9F43"}
	brandDeep   = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#F2530D"}
	brandInk    = lipgloss.Color("#1A1008")
	brandMuted  = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#8C8279"}
)

// wizardBanner is the pixel logo with the tagline beside the bowl of the q.
func wizardBanner() string {
	title := lipgloss.NewStyle().Foreground(brandOrange).Bold(true).Render("qc · fast video quality analysis")
	subtitle := lipgloss.NewStyle().Foreground(brandMuted).Render("technical metrics · VMAF · per-title ladders")

	return "\n" + tui.Logo("", "", title, subtitle) + "\n"
}

// wizardTheme is huh's Charm theme in the brand's oranges instead of its
// indigo and fuchsia.
func wizardTheme() *huh.Theme {
	t := huh.ThemeCharm()

	t.Focused.Base = t.Focused.Base.BorderForeground(brandOrange)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(brandOrange)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(brandOrange)
	t.Focused.Directory = t.Focused.Directory.Foreground(brandOrange)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(brandDeep)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(brandDeep)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(brandDeep)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(brandDeep)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(brandInk).Background(brandOrange).Bold(true)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(brandDeep)

	// Blurred fields derive from the focused ones, as in ThemeCharm.
	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	return t
}
