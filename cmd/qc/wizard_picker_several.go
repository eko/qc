package main

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/eko/qc/ladder"
)

// Several lets the picker take several videos, for one ladder or one sample
// for all of them: space selects the highlighted one, enter moves on with those
// selected. The first stays the value of the picker, more receives the
// others.
func (f *videoPicker) Several(
	more *[]string,
) *videoPicker {
	f.more = more

	return f
}

// restore selects again the videos picked before, when there were several.
func (f *videoPicker) restore() {
	if f.more == nil || len(*f.more) == 0 {
		return
	}

	f.marked = []string{f.absolute(*f.value)}
	for _, path := range *f.more {
		f.marked = append(f.marked, f.absolute(path))
	}
}

// isMarked reports whether the video at path is selected.
func (f *videoPicker) isMarked(
	path string,
) bool {
	return slices.Contains(f.marked, path)
}

// toggle selects the highlighted video, or takes it out of the selection.
func (f *videoPicker) toggle() tea.Cmd {
	e, ok := f.current()
	if !ok || e.dir {
		return nil
	}

	path := f.currentPath()

	if i := slices.Index(f.marked, path); i >= 0 {
		f.marked = slices.Delete(f.marked, i, i+1)

		return nil
	}

	if _, known := f.cache.cached(path); known {
		f.mark(path)

		return nil
	}

	f.pending, f.marking = path, true

	return f.probe(path)
}

// mark selects path when it can share a ladder with the videos selected.
func (f *videoPicker) mark(
	path string,
) {
	if err := f.alike(path, f.marked); err != nil {
		f.err = err

		return
	}

	f.marked = append(f.marked, path)
}

// alike rejects a video the run cannot use, or whose format is not that of
// the first of others: the ladder would refuse them once started.
func (f *videoPicker) alike(
	path string,
	others []string,
) error {
	sum := f.cache.inspect(path)
	if err := sum.validate(); err != nil {
		return err
	}

	if len(others) == 0 {
		return nil
	}

	if diff := ladder.FormatDifference(f.cache.inspect(others[0]).stream, sum.stream); diff != "" {
		return fmt.Errorf("not %s like %s: one ladder needs videos of one format", diff, filepath.Base(others[0]))
	}

	return nil
}

// confirm moves on with the selected videos.
func (f *videoPicker) confirm() tea.Cmd {
	*f.value = f.relative(f.marked[0])
	*f.more = nil

	for _, path := range f.marked[1:] {
		*f.more = append(*f.more, f.relative(path))
	}

	return huh.NextField
}

// selectionView tells how many videos are selected and what enter does,
// or that several can be.
func (f *videoPicker) selectionView() string {
	s := f.style
	dot := " " + s.g.dot + " "

	if len(f.marked) == 0 {
		return s.faint.Render("space selects several videos: one ladder, or one sample, for all of them")
	}

	if len(f.marked) == 1 {
		return s.success.Render(s.g.check+" 1 video selected") + s.faint.Render(dot+"space adds another"+dot+"enter continues")
	}

	return s.success.Render(fmt.Sprintf("%s %d videos selected", s.g.check, len(f.marked))) +
		s.faint.Render(dot+"one ladder or sample for all of them"+dot+"enter continues")
}

// askMore asks for the other videos of the ladder, one path a line, until
// an empty line.
func (f *videoPicker) askMore(
	w io.Writer,
	scanner *bufio.Scanner,
) error {
	picked := []string{*f.value}

	for {
		_, _ = fmt.Fprint(w, "Another video, for one ladder or sample of them all (path, empty to continue): ")
		if !scanner.Scan() {
			_, _ = fmt.Fprintln(w)

			return io.ErrUnexpectedEOF
		}

		path := strings.TrimSpace(scanner.Text())
		if path == "" {
			*f.more = nil
			if len(picked) > 1 {
				*f.more = picked[1:]
			}

			return nil
		}

		err := f.check(path)
		if err == nil {
			err = f.alike(path, picked)
		}

		if err != nil {
			_, _ = fmt.Fprintln(w, err)

			continue
		}

		picked = append(picked, path)
	}
}
