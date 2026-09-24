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
	// dashboard is true when stderr is a terminal: commands draw the live
	// dashboard there.
	dashboard bool
	// wizard is true when stdin and stderr are terminals: qc without
	// arguments starts the interactive wizard instead of printing the help.
	wizard bool
	// width is the width of the text reports.
	width int
	// askWizard runs the interactive wizard form, with the GPU question
	// when offerGPU.
	askWizard func(offerGPU bool) (wizardAnswers, error)
	// gpuAvailable reports whether the ffmpeg binary can use an NVIDIA GPU
	// (nvidia.Available); nil offers no GPU.
	gpuAvailable func(ctx context.Context, ffmpeg string) bool
}

// detectEnvironment inspects the standard streams of the process.
func detectEnvironment() environment {
	width := defaultWidth
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		width = w
	}

	stderr := term.IsTerminal(int(os.Stderr.Fd()))

	return environment{
		dashboard:    stderr,
		wizard:       stderr && term.IsTerminal(int(os.Stdin.Fd())),
		width:        width,
		askWizard:    askWizard,
		gpuAvailable: nvidia.Available,
	}
}
