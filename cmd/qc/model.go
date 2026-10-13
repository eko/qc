package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/tui"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/ladder/corpus"
	"github.com/eko/qc/ladder/features"
	"github.com/eko/qc/ladder/predict"
)

// ErrNoModelFile is returned when model train is not told where to write.
var ErrNoModelFile = errors.New("--to is required: the file the model is written to")

// ErrAnchorCRFs is returned for --anchor-crfs not made of two CRFs.
var ErrAnchorCRFs = errors.New("--anchor-crfs takes three CRFs: low, high and far")

// anchorCRFCount is the number of CRFs of --anchor-crfs.
const anchorCRFCount = 3

func newModelCommand(
	env environment,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "Measure a corpus and train the ladder models that complete the probes of a ladder (qc ladder --ladder-model)",
		// Flags are those of the subcommands: a mistyped subcommand is
		// then reported as such, not as the first of its flags.
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		Args:               subcommandOnly,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newModelCorpusCommand(env), newModelTrainCommand(), newModelReportCommand())

	return cmd
}

// ErrUnknownSubcommand is returned for a subcommand a command has not.
var ErrUnknownSubcommand = errors.New("unknown command")

// subcommandOnly rejects arguments of a command that only has
// subcommands: the first one is a mistyped subcommand, named with the
// nearest ones.
func subcommandOnly(
	cmd *cobra.Command,
	args []string,
) error {
	if len(args) == 0 {
		return nil
	}

	// Cobra sets the distance of its suggestions when it looks a command
	// up, which it did not do for this argument.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}

	hint := ""
	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		hint = fmt.Sprintf(" (did you mean %q?)", cmd.CommandPath()+" "+suggestions[0])
	}

	return fmt.Errorf("%w %q for %q%s", ErrUnknownSubcommand, args[0], cmd.CommandPath(), hint)
}

// ErrNoVideos is returned for a folder given to model corpus that holds
// no video, subfolders included.
var ErrNoVideos = errors.New("no video in the folder")

// corpusSources returns the videos to measure: the files given, and the
// videos of the folders given and of their subfolders, in path order.
// Hidden files and folders are left out.
func corpusSources(
	args []string,
) ([]string, error) {
	var sources []string

	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil || !info.IsDir() {
			// A missing file is reported when it is inspected.
			sources = append(sources, arg)

			continue
		}

		videos, err := videosUnder(arg)
		if err != nil {
			return nil, err
		}

		if len(videos) == 0 {
			return nil, fmt.Errorf("%s: %w", arg, ErrNoVideos)
		}

		sources = append(sources, videos...)
	}

	return sources, nil
}

// videosUnder lists the videos of dir and of its subfolders, in path
// order, hidden files and folders left out.
func videosUnder(
	dir string,
) ([]string, error) {
	var videos []string

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		hidden := strings.HasPrefix(entry.Name(), ".") && path != dir

		switch {
		case entry.IsDir() && hidden:
			return filepath.SkipDir
		case !entry.IsDir() && !hidden && isVideoName(entry.Name()):
			videos = append(videos, path)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	return videos, nil
}

// ErrNoCorpusFile is returned when model corpus is not told where to
// write.
var ErrNoCorpusFile = errors.New("--to is required: the corpus file the measurements are added to")

func newModelCorpusCommand(
	env environment,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "corpus <source or folder>...",
		Short: "Measure titles on a grid of resolutions and CRFs: what a ladder model is trained on",
		Long: "Measure what a ladder model learns from. An excerpt of each title (segments of 2 s taken\n" +
			"evenly across it) is encoded at every resolution and CRF of a grid, as the probes of a\n" +
			"ladder are, and every frame is scored: each segment gets its bitrate and its VMAF at\n" +
			"every point of the grid. The excerpt is also described by its analysis (spatial and\n" +
			"temporal information, camera motion, what a very fast encode spends on it, what scaling\n" +
			"loses): the features a model needs to predict a ladder without encoding (--instant).\n\n" +
			"The measurements are added to the corpus file as they come (JSON lines); encodes the\n" +
			"file already holds are not measured again, so an interrupted run resumes, and titles\n" +
			"or codecs can be added to a corpus later. A title costs 55 encodes of its excerpt with\n" +
			"every frame scored: about half an hour for two minutes of 1080p with x264.\n\n" +
			"A folder stands for every video in it and in its subfolders.\n\n" +
			"Titles of one corpus must share their resolution; the more they differ in content\n" +
			"(drama, animation, sport, grain), the better the model holds on a new title.",
		Example: "  qc model corpus films/ --to corpus.jsonl\n" +
			"  qc model corpus films/*.mov --to corpus.jsonl -c av1\n" +
			"  qc model train corpus.jsonl -c h264 --to h264-fast.json",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModelCorpus(cmd, env, args)
		},
	}

	flags := cmd.Flags()
	flags.StringP("codec", "c", "h264", "codec of the encodes: h264, hevc or av1")
	flags.String("preset", "", "encoder preset of the encodes (default: the codec's ladder preset)")
	flags.Int("segments", corpus.DefaultSegments, "segments of 2 s measured in each title, taken evenly across it")
	flags.IntSlice("heights", ladder.DefaultHeights(), "resolutions of the grid (heights above the source are dropped)")
	flags.Int("crf-count", 0, "number of CRFs of the grid, spread evenly over the codec's range (default 0: the codec's grid, 11 CRFs from 13 to 48 for H.264, 9 from 16 to 63 for AV1); fewer measure faster, more read finer between them; keep one count for a whole corpus")
	flags.Float64Slice("crfs", nil, "CRFs of the grid, listed (in place of --crf-count)")
	flags.String("to", "", "the corpus file the measurements are added to")
	addGPUFlags(cmd, gpuDecode)

	return cmd
}

// runModelCorpus measures the sources on the grid the flags of cmd
// describe and adds the measurements to the corpus file.
func runModelCorpus(
	cmd *cobra.Command,
	env environment,
	sources []string,
) error {
	config, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	to, _ := cmd.Flags().GetString("to")
	if to == "" {
		return ErrNoCorpusFile
	}

	opts, err := corpusOptions(cmd, to)
	if err != nil {
		return err
	}

	if sources, err = corpusSources(sources); err != nil {
		return err
	}

	labels := make([]string, len(sources))
	for i, source := range sources {
		labels[i] = filepath.Base(source)
	}

	added := 0

	var failed []string

	err = runDashboard(cmd, env, config, "model corpus", fmt.Sprintf("%s → %s", plural(len(sources), "title"), to), labels,
		func(ctx context.Context, svc services, e *tui.Emitter) (err error) {
			added, failed, err = measureTitles(ctx, svc.corpus, e, sources, to, opts)

			return err
		})
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%d measurements added to %s\n", added, to)

	if len(failed) > 0 {
		return fmt.Errorf("%w: %d of %d:\n  %s", ErrTitlesFailed, len(failed), len(sources), strings.Join(failed, "\n  "))
	}

	return nil
}

// ErrTitlesFailed is returned when titles of a corpus could not be
// measured; the others were.
var ErrTitlesFailed = errors.New("titles could not be measured")

// measureTitles measures the sources one after the other into the corpus
// file at path, each a stage of the dashboard. It returns the number of
// measurements added and the titles that failed, each with its error: one
// title that cannot be measured does not stop a corpus of hours. A run
// that was stopped returns its error; what it measured is kept.
func measureTitles(
	ctx context.Context,
	builder *corpus.Builder,
	e *tui.Emitter,
	sources []string,
	path string,
	opts corpus.Options,
) (int, []string, error) {
	added := 0

	var failed []string

	for i, source := range sources {
		e.Start(i)

		measured, err := measureTitle(ctx, builder, source, path, opts, func(done, total int) { e.Progress(i, done, total, "") })
		added += measured

		switch {
		case err != nil && ctx.Err() != nil:
			return added, failed, err
		case err != nil:
			failed = append(failed, fmt.Sprintf("%s: %v", filepath.Base(source), err))
			e.Fail(i, "failed")
		default:
			e.Done(i, fmt.Sprintf("%d measurements", measured))
		}
	}

	return added, failed, nil
}

// measureTitle measures source into the corpus file at path and returns
// the number of measurements it added (its features, and the encodes of
// its grid); progress follows the encodes of its
// grid.
func measureTitle(
	ctx context.Context,
	builder *corpus.Builder,
	source, path string,
	opts corpus.Options,
	progress func(done, total int),
) (int, error) {
	added := 0

	opts.Progress = func(p corpus.Progress) {
		if p.Stage == corpus.StageGrid {
			progress(p.Done, p.Total)
		}
	}

	err := builder.Measure(ctx, source, opts, func(row predict.Row) error {
		added++

		return appendRow(path, row)
	})

	return added, err
}

// ErrCRFFlags is returned when the CRFs of a corpus are both counted and
// listed.
var ErrCRFFlags = errors.New("--crf-count and --crfs are two ways to choose the CRFs: give one")

// corpusOptions maps the flags of model corpus to the options of a
// measurement, which skips the encodes the corpus file at path holds.
func corpusOptions(
	cmd *cobra.Command,
	path string,
) (corpus.Options, error) {
	flags := cmd.Flags()

	opts := corpus.Options{}
	opts.Codec, _ = flags.GetString("codec")
	opts.Preset, _ = flags.GetString("preset")
	opts.Segments, _ = flags.GetInt("segments")
	opts.Heights, _ = flags.GetIntSlice("heights")
	opts.CRFs, _ = flags.GetFloat64Slice("crfs")

	codec, err := encode.Lookup(opts.Codec)
	if err != nil {
		return corpus.Options{}, err
	}

	if count, _ := flags.GetInt("crf-count"); count != 0 {
		if len(opts.CRFs) > 0 {
			return corpus.Options{}, ErrCRFFlags
		}

		if opts.CRFs, err = corpus.SpreadCRFs(codec, count); err != nil {
			return corpus.Options{}, err
		}
	}

	measured, err := predict.ReadRows(path)
	if err != nil {
		return corpus.Options{}, err
	}

	opts.Measured = func(row predict.Row) bool {
		return slices.ContainsFunc(measured, row.Same)
	}

	return opts, nil
}

// appendRow adds row to the corpus file at path.
func appendRow(
	path string,
	row predict.Row,
) error {
	line, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode corpus row: %w", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("write corpus: %w", err)
	}

	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()

		return fmt.Errorf("write corpus: %w", err)
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("write corpus: %w", err)
	}

	return nil
}

func newModelTrainCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "train <corpus.jsonl>...",
		Short: "Train a ladder model on a corpus of measured titles",
		Long: "Train a ladder model on a corpus: titles measured on a whole grid of resolutions and\n" +
			"CRFs, segment by segment. The model learns how the bitrate and the VMAF of one encoder\n" +
			"at every point of that grid follow from a few measured encodes (the anchors), so that\n" +
			"qc ladder --ladder-model measures those alone.\n\n" +
			"A model is for one codec and one preset, and for sources of one height. It holds\n" +
			"several designs, from two anchors per resolution down to a single anchor, and down to\n" +
			"none when the corpus holds the features of its titles: the ladder is then predicted from\n" +
			"the analysis of the video alone (qc ladder --instant). Each is\n" +
			"validated by leaving every title of the corpus out in turn: the errors printed are\n" +
			"those made on titles the model had not seen, and a ladder build picks the design with\n" +
			"the fewest anchors whose mean error is within its tolerance.",
		Example: "  qc model train corpus/*.jsonl -c h264 --to h264-fast.json\n" +
			"  qc ladder source.mov -c h264 --ladder-model h264-fast.json",
		Args: cobra.MinimumNArgs(1),
		RunE: runModelTrain,
	}

	flags := cmd.Flags()
	flags.StringP("codec", "c", "h264", "codec of the model: h264, hevc or av1")
	flags.String("preset", "", "encoder preset of the model (default: the codec's ladder preset)")
	flags.Float64Slice("anchor-crfs", nil, "the three CRFs the anchors are measured at: two alternating between resolutions, and a far one for the top resolutions (default: halfway between the codec's probe CRFs, and one probe step beyond the last)")
	flags.String("to", "", "the file the model is written to")

	return cmd
}

// runModelTrain trains the model the flags of cmd describe on the corpus
// files and prints the validation of its designs.
func runModelTrain(
	cmd *cobra.Command,
	paths []string,
) error {
	flags := cmd.Flags()
	codecName, _ := flags.GetString("codec")
	preset, _ := flags.GetString("preset")
	anchorCRFs, _ := flags.GetFloat64Slice("anchor-crfs")
	to, _ := flags.GetString("to")

	if to == "" {
		return ErrNoModelFile
	}

	codec, err := encode.Lookup(codecName)
	if err != nil {
		return err
	}

	if preset == "" {
		preset = codec.DefaultPreset
	}

	if anchorCRFs, err = anchorCRFsOf(codec, anchorCRFs); err != nil {
		return err
	}

	titles, err := predict.ReadCorpus(codec.Name, preset, paths...)
	if err != nil {
		return err
	}

	if len(titles) == 0 {
		return fmt.Errorf("%w: no %s %s encodes in %s", predict.ErrCorpus, codec.Name, preset, strings.Join(paths, ", "))
	}

	// A corpus still being measured holds a title not finished yet: the
	// model is trained on the others.
	titles, partial := predict.Complete(titles)

	model, err := predict.Train(titles, predict.TrainOptions{
		LowCRF: anchorCRFs[0], HighCRF: anchorCRFs[1], FarCRF: anchorCRFs[2],
		FeatureSets: features.Sets(slices.Sorted(maps.Keys(titles[0].Features))),
	})
	if err != nil {
		return err
	}

	if err := model.Save(to); err != nil {
		return err
	}

	if err := renderModel(cmd.OutOrStdout(), model, to); err != nil {
		return err
	}

	for _, t := range partial {
		fmt.Fprintf(cmd.OutOrStdout(), "Left out: %s, measured at %d points of the grid of %d (still being measured, or interrupted: qc model corpus finishes it).\n",
			t.Name, len(t.Points), len(model.Heights)*len(model.CRFs))
	}

	return nil
}

// anchorCRFsOf returns the three CRFs the anchors of a model of codec are
// measured at: those given, or halfway between the codec's probe CRFs and
// one probe step beyond the last.
func anchorCRFsOf(
	codec encode.Codec,
	given []float64,
) ([]float64, error) {
	switch len(given) {
	case 0:
		probes := codec.ProbeCRFs

		return []float64{(probes[0] + probes[1]) / 2, (probes[1] + probes[2]) / 2, 2*probes[2] - probes[1]}, nil
	case anchorCRFCount:
		return given, nil
	default:
		return nil, fmt.Errorf("%w: %v", ErrAnchorCRFs, given)
	}
}

// instantLabel names a design without anchor by what it reads of a title:
// the two fast encodes of the analysis or not, the source's own encode or
// not.
func instantLabel(
	d predict.Design,
) string {
	label := "the analysis alone (--instant)"
	if !slices.ContainsFunc(d.Features, features.NeedsEncode) {
		label = "the frames alone (--instant --no-encode)"
	}

	if !slices.Contains(d.Features, features.SourceQP) {
		label += ", source quantisers unknown"
	}

	return label
}

// renderModel prints what a model was trained on and how each of its
// designs did on the titles left out of its training.
func renderModel(
	w io.Writer,
	model *predict.Model,
	path string,
) error {
	fmt.Fprintf(w, "%s %s, %dp sources: %d titles, %d segments, grid of %d resolutions × %d CRFs\n\n",
		model.Codec, model.Preset, model.SourceHeight, model.Titles, model.Segments, len(model.Heights), len(model.CRFs))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "anchors\tresolution@CRF\tVMAF error (mean)\t(worst)\tbitrate error (mean)")

	for _, d := range model.Designs {
		points := make([]string, len(d.Anchors))
		for i, a := range d.Anchors {
			points[i] = fmt.Sprintf("%dp@%g", a.Height, a.CRF)
		}

		at := strings.Join(points, " ")

		switch {
		case len(d.Features) > 0 && len(d.Anchors) == 0:
			at = instantLabel(d)
		case len(d.Features) > 0:
			at += " + the analysis"
		}

		if v := d.Validation; v != nil {
			fmt.Fprintf(tw, "%d\t%s\t%.2f\t%.2f\t%.1f%%\n", len(d.Anchors), at, v.MeanVMAFError, v.WorstVMAFError, v.MeanBitrateError*100)
		} else {
			fmt.Fprintf(tw, "%d\t%s\t-\t-\t-\n", len(d.Anchors), at)
		}
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	if model.Titles < 2 {
		fmt.Fprintln(w, "\nNot validated: leaving a title out needs at least two.")
	} else {
		fmt.Fprintln(w, "\nErrors on digests of titles left out of the training, over the qualities a ladder uses.")

		if instant, ok := model.Instant(nil); ok && instant.Validation != nil {
			fmt.Fprintf(w, "From the analysis alone, without any probe: VMAF within %.1f on average of a title the model has not seen.\n",
				instant.Validation.MeanVMAFError)
		} else if !ok {
			fmt.Fprintln(w, "No design from the analysis alone: the corpus holds no features of some title (qc model corpus adds them).")
		}
	}

	fmt.Fprintf(w, "Model written to %s\n", path)

	return nil
}
