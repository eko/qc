package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/eko/qc/internal/tui"
)

// emit writes the JSON and HTML (with renderHTML, the page of the report's
// kind) files when requested, then prints the report on w: as JSON, or with
// renderText followed by the list of written files.
func emit[T any](
	w io.Writer,
	config OutputConfig,
	report T,
	renderText func(w io.Writer) error,
	renderHTML func(w io.Writer, report T) error,
) error {
	if config.Output != "" {
		if err := writeFile(config.Output, func(f io.Writer) error { return writeJSON(f, report) }); err != nil {
			return err
		}
	}

	if config.HTML != "" {
		if err := writeFile(config.HTML, func(f io.Writer) error { return renderHTML(f, report) }); err != nil {
			return err
		}
	}

	if config.Format == formatJSON {
		return writeJSON(w, report)
	}

	if err := renderText(w); err != nil {
		return err
	}

	return tui.RenderWritten(w, config.Output, config.HTML)
}

// writeFile creates path and fills it with write.
func writeFile(
	path string,
	write func(w io.Writer) error,
) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}

	if err := write(f); err != nil {
		_ = f.Close()

		return fmt.Errorf("write %s: %w", path, err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	return nil
}

// writeJSON writes report as indented JSON.
func writeJSON(
	w io.Writer,
	report any,
) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("encode json report: %w", err)
	}

	return nil
}
