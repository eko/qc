// Package termimg shows pictures in terminals that draw them: inline
// images by the protocol of iTerm2 (iTerm2, WezTerm, VS Code) or of Kitty
// (Kitty, Ghostty, Warp). Other terminals get nothing from it, and the
// caller draws its text instead.
package termimg

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Protocol is how a terminal is told to draw a picture.
type Protocol string

// Protocols of inline images.
const (
	// None draws no picture: the caller writes text.
	None Protocol = ""
	// ITerm is the inline image escape of iTerm2 (OSC 1337).
	ITerm Protocol = "iterm"
	// Kitty is the graphics protocol of Kitty (APC G).
	Kitty Protocol = "kitty"
)

// Settings of Detect, from the environment.
const (
	// ChartsVariable forces the protocol: "iterm", "kitty", or "text"
	// for none, whatever the terminal.
	ChartsVariable = "QC_CHARTS"
	textCharts     = "text"
)

// ErrProtocol is returned for a value of QC_CHARTS that names no
// protocol.
var ErrProtocol = errors.New("unknown terminal charts: use iterm, kitty or text")

// itermTerminals and kittyTerminals are the TERM_PROGRAM values of the
// terminals drawing each protocol.
var (
	itermTerminals = []string{"iTerm.app", "WezTerm", "vscode", "Tabby", "Hyper", "rio"}
	kittyTerminals = []string{"ghostty", "kitty", "WarpTerminal"}
)

// Detect returns the protocol the terminal of the environment draws
// pictures by: the one QC_CHARTS names, else the one of the terminal
// TERM_PROGRAM, TERM or KITTY_WINDOW_ID tell. Inside tmux or screen, which
// do not pass pictures on unless configured to, it is None.
func Detect(
	getenv func(string) string,
) (Protocol, error) {
	switch forced := strings.ToLower(strings.TrimSpace(getenv(ChartsVariable))); forced {
	case "", "auto":
	case textCharts:
		return None, nil
	case string(ITerm), string(Kitty):
		return Protocol(forced), nil
	default:
		return None, fmt.Errorf("%s=%q: %w", ChartsVariable, forced, ErrProtocol)
	}

	if getenv("TMUX") != "" || strings.HasPrefix(getenv("TERM"), "screen") {
		return None, nil
	}

	program, term := getenv("TERM_PROGRAM"), getenv("TERM")

	for _, name := range kittyTerminals {
		if strings.EqualFold(program, name) || strings.Contains(term, name) {
			return Kitty, nil
		}
	}

	if getenv("KITTY_WINDOW_ID") != "" {
		return Kitty, nil
	}

	for _, name := range itermTerminals {
		if strings.EqualFold(program, name) {
			return ITerm, nil
		}
	}

	if getenv("LC_TERMINAL") == "iTerm2" {
		return ITerm, nil
	}

	return None, nil
}

// kittyChunk is the most base64 a Kitty escape carries at once.
const kittyChunk = 4096

// Write draws the PNG picture png cols character cells wide, as tall as
// its proportions make it, and moves the cursor under it.
func Write(
	w io.Writer,
	png []byte,
	cols int,
	p Protocol,
) error {
	data := base64.StdEncoding.EncodeToString(png)

	var b strings.Builder

	switch p {
	case ITerm:
		fmt.Fprintf(&b, "\x1b]1337;File=inline=1;size=%d;width=%d;preserveAspectRatio=1:%s\a\n", len(png), cols, data)
	case Kitty:
		for start := 0; start < len(data); start += kittyChunk {
			end := min(start+kittyChunk, len(data))

			more := 1
			if end == len(data) {
				more = 0
			}

			if start == 0 {
				fmt.Fprintf(&b, "\x1b_Ga=T,f=100,q=2,c=%d,m=%d;%s\x1b\\", cols, more, data[start:end])
			} else {
				fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, data[start:end])
			}
		}

		b.WriteString("\n")
	default:
		return nil
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("draw picture: %w", err)
	}

	return nil
}

// Place returns the Kitty escape drawing the PNG picture png as image id,
// stretched over cols columns and rows rows from the cursor, which stays
// where it is: a program redrawing its screen line by line keeps its
// count of lines, and draws the rows under the picture blank. Sent again
// with the same id, the picture replaces the previous one in place.
func Place(
	png []byte,
	cols, rows int,
	id uint32,
) string {
	data := base64.StdEncoding.EncodeToString(png)

	var b strings.Builder

	for start := 0; start < len(data); start += kittyChunk {
		end := min(start+kittyChunk, len(data))

		more := 1
		if end == len(data) {
			more = 0
		}

		if start == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,f=100,q=2,C=1,i=%d,p=1,c=%d,r=%d,m=%d;%s\x1b\\", id, cols, rows, more, data[start:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, data[start:end])
		}
	}

	return b.String()
}

// Delete returns the Kitty escape removing image id and its placements.
func Delete(
	id uint32,
) string {
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
}
