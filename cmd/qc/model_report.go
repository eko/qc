package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/htmlreport"
	"github.com/eko/qc/ladder/predict"
)

// ErrNoReportFile is returned when model report is not told where to
// write.
var ErrNoReportFile = errors.New("--to is required: the HTML file the report is written to")

// ErrNothingToReport is returned when model report is given neither a
// corpus nor a model.
var ErrNothingToReport = errors.New("nothing to report: give corpus files, --model, or both")

func newModelReportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report [corpus.jsonl]...",
		Short: "Write an HTML report of a ladder model, of the corpus it is trained on, or of both",
		Long: "Write a self-contained HTML page showing what a ladder model is worth and what its\n" +
			"corpus holds, before any ladder is built with it.\n\n" +
			"Of a model (--model): whether it predicts without a probe (--instant) and without any\n" +
			"encode (--no-encode), the error of each of its designs on titles it had not seen, where\n" +
			"on the grid of resolutions and CRFs they are wrong, and which titles they read worst.\n\n" +
			"Of a corpus (the files given): the VMAF and the bitrate of its titles over the grid as\n" +
			"two surfaces to turn and to go through title by title, their rate-quality curves, and\n" +
			"where the titles sit on each feature a model reads of them: the kinds of content the\n" +
			"corpus lacks.\n\n" +
			"With both, the report also tells a model older than its corpus, and a grid that stops\n" +
			"before the low rungs of its titles.",
		Example: "  qc model report corpus.jsonl --model h264-fast.json --to model.html\n" +
			"  qc model report corpus.jsonl -c av1 --to corpus-av1.html\n" +
			"  qc model report --model h264-fast.json --to model.html",
		Args: cobra.ArbitraryArgs,
		RunE: runModelReport,
	}

	flags := cmd.Flags()
	flags.String("model", "", "the model to report, trained by qc model train")
	flags.StringP("codec", "c", "h264", "codec of the corpus to report, without --model: h264, hevc or av1")
	flags.String("preset", "", "encoder preset of the corpus to report, without --model (default: the codec's ladder preset)")
	flags.String("to", "", "the HTML file the report is written to")

	return cmd
}

// runModelReport writes the report of the model and of the corpus files
// the flags of cmd and paths name.
func runModelReport(
	cmd *cobra.Command,
	paths []string,
) error {
	flags := cmd.Flags()
	modelPath, _ := flags.GetString("model")
	to, _ := flags.GetString("to")

	switch {
	case modelPath == "" && len(paths) == 0:
		return ErrNothingToReport
	case to == "":
		return ErrNoReportFile
	}

	report, err := modelReport(cmd, modelPath, paths)
	if err != nil {
		return err
	}

	err = writeFile(to, func(w io.Writer) error {
		return htmlreport.RenderModel(w, report)
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Report written to %s\n", to)

	return nil
}

// modelReport reads what a report shows: the model at modelPath, when
// there is one, and the titles the corpus files at paths hold for its
// encoder, or for the one the flags of cmd name.
func modelReport(
	cmd *cobra.Command,
	modelPath string,
	paths []string,
) (htmlreport.ModelReport, error) {
	report := htmlreport.ModelReport{ModelPath: modelPath, Corpus: paths}
	report.Codec, _ = cmd.Flags().GetString("codec")
	report.Preset, _ = cmd.Flags().GetString("preset")

	if modelPath != "" {
		model, err := predict.Load(modelPath)
		if err != nil {
			return htmlreport.ModelReport{}, err
		}

		// A model is for one encoder: its corpus is read for that one.
		report.Model, report.Codec, report.Preset = model, model.Codec, model.Preset
	}

	codec, err := encode.Lookup(report.Codec)
	if err != nil {
		return htmlreport.ModelReport{}, err
	}

	if report.Preset == "" {
		report.Preset = codec.DefaultPreset
	}

	if len(paths) == 0 {
		return report, nil
	}

	titles, err := predict.ReadCorpus(codec.Name, report.Preset, paths...)
	if err != nil {
		return htmlreport.ModelReport{}, err
	}

	if len(titles) == 0 {
		return htmlreport.ModelReport{}, fmt.Errorf("%w: no %s %s encodes in %s", predict.ErrCorpus, codec.Name, report.Preset, strings.Join(paths, ", "))
	}

	report.Titles, report.Partial = predict.Complete(titles)

	return report, nil
}
