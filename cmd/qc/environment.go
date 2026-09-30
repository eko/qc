package main

import (
	"context"
	"os"

	"golang.org/x/term"

	"github.com/eko/qc/nvidia"
)

// defaultWidth is the width of text reports when stdout is not a terminal.
const defaultWidth = 100

// environment is what the commands need from the process they run in.
type environment struct {
	// dashboard is true when stderr is a terminal qc is in the foreground
	// of: commands draw the live dashboard there.
	dashboard bool
	// wizard is true when stdin and stderr are terminals: qc without
	// arguments starts the interactive wizard instead of printing the help.
	wizard bool
	// width is the width of the text reports.
	width int
	// askWizard runs the interactive wizard form (see wizardContext).
	askWizard func(ctx wizardContext) (wizardAnswers, error)
	// gpuAvailable reports whether the ffmpeg binary can use an NVIDIA GPU
	// (nvidia.Available); nil offers no GPU.
	gpuAvailable func(ctx context.Context, ffmpeg string) bool
	// animate lets the wizard play its short logo animation: stderr is a
	// terminal and neither colours nor motion were turned off.
	animate bool
}

// detectEnvironment inspects the standard streams of the process.
func detectEnvironment() environment {
	width := defaultWidth
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}

	stderr := term.IsTerminal(int(os.Stderr.Fd()))

	return environment{
		dashboard:    stderr && inForeground(int(os.Stderr.Fd())),
		wizard:       stderr && term.IsTerminal(int(os.Stdin.Fd())),
		width:        width,
		askWizard:    askWizard,
		gpuAvailable: nvidia.Available,
		animate:      stderr && motionAllowed(os.Getenv),
	}
}

// foreground reports whether the terminal's foreground process group
// (tcgetpgrp) is the process's own.
func foreground(
	terminalGroup func() (int, error),
	ownGroup func() int,
) bool {
	group, err := terminalGroup()

	return err == nil && group == ownGroup()
}

// motionAllowed reports whether animations may play: not without colours
// (NO_COLOR, TERM=dumb), in the accessible mode, or when QC_NO_ANIMATION
// asks for a still interface.
func motionAllowed(
	getenv func(string) string,
) bool {
	for _, name := range []string{"NO_COLOR", "ACCESSIBLE", "QC_NO_ANIMATION"} {
		if getenv(name) != "" {
			return false
		}
	}

	return getenv("TERM") != "dumb"
}
