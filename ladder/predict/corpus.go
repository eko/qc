package predict

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Kinds of the rows of a corpus.
const (
	// GridKind is an encode of the excerpt of a title with every frame
	// scored: what a model learns to predict.
	GridKind = "grid"
	// FeaturesKind holds the features of the segments of the excerpt (see
	// package features): what a model may predict from.
	FeaturesKind = "features"
)

// defaultSegmentSeconds is the length of the segments of a row that does
// not give it.
const defaultSegmentSeconds = 2

// Row is one line of a corpus file (JSON lines, see package corpus): one
// encode of the excerpt of a title, read segment by segment.
type Row struct {
	Kind   string  `json:"kind"`
	Source string  `json:"source"`
	Codec  string  `json:"codec"`
	Preset string  `json:"preset"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	CRF    float64 `json:"crf"`
	// SourceHeight is the height of the title; the highest of its grid
	// when zero.
	SourceHeight int `json:"sourceHeight,omitempty"`
	// SegmentSeconds is the length of each segment. Default 2.
	SegmentSeconds float64 `json:"segmentSeconds,omitempty"`
	// Segments is the number of segments of the excerpt; zero in rows
	// written before it was, which say it by the length of their
	// measurements.
	Segments int `json:"segments,omitempty"`
	// Bits is the size of each segment and VMAF the mean VMAF of its
	// frames.
	Bits []int64   `json:"bits,omitempty"`
	VMAF []float64 `json:"vmaf,omitempty"`
	// Features are the features of each segment, by name (FeaturesKind),
	// and Version the version of the set they are (see package features):
	// a corpus described by an older version is described again, and the
	// last description of a title is the one read.
	Features map[string][]float64 `json:"features,omitempty"`
	Version  int                  `json:"version,omitempty"`
}

// ReadCorpus reads the corpus files at paths and returns the titles
// encoded by codec at preset, one per source, in the order they first
// appear. Rows of another kind, codec or preset are left out.
func ReadCorpus(
	codec, preset string,
	paths ...string,
) ([]Title, error) {
	var titles []Title

	features := map[string]map[string][]float64{}

	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("read corpus: %w", err)
		}

		titles, err = readRows(file, codec, preset, titles, features)
		_ = file.Close()

		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	for i := range titles {
		titles[i].Features = features[titles[i].Name]

		if titles[i].SourceHeight == 0 {
			for _, p := range titles[i].Points {
				titles[i].SourceHeight = max(titles[i].SourceHeight, p.Height)
			}
		}
	}

	return titles, nil
}

// readRows adds the grid rows of r for codec at preset to titles, and the
// features of the titles of r, whatever the codec, to features.
func readRows(
	r io.Reader,
	codec, preset string,
	titles []Title,
	features map[string]map[string][]float64,
) ([]Title, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, 64<<20)

	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}

		var row Row
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCorpus, err)
		}

		if row.Kind == FeaturesKind {
			features[titleName(row.Source)] = row.Features

			continue
		}

		if row.Kind != GridKind || row.Codec != codec || row.Preset != preset {
			continue
		}

		if len(row.Bits) == 0 || len(row.Bits) != len(row.VMAF) {
			return nil, fmt.Errorf("%w: %s at %dp CRF %g has %d sizes and %d scores", ErrCorpus, row.Source, row.Height, row.CRF, len(row.Bits), len(row.VMAF))
		}

		i := slices.IndexFunc(titles, func(t Title) bool { return t.Name == titleName(row.Source) })
		if i < 0 {
			titles = append(titles, Title{Name: titleName(row.Source), Codec: codec, Preset: preset, SourceHeight: row.SourceHeight})
			i = len(titles) - 1
		}

		// A point measured again (its excerpt was cut another way)
		// replaces the earlier measurement.
		point := row.point()
		if k := slices.IndexFunc(titles[i].Points, func(tp TitlePoint) bool { return tp.Point == point.Point }); k >= 0 {
			titles[i].Points[k] = point
		} else {
			titles[i].Points = append(titles[i].Points, point)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorpus, err)
	}

	return titles, nil
}

// point is the row as the measurements of a title at its point.
func (r Row) point() TitlePoint {
	seconds := r.SegmentSeconds
	if seconds <= 0 {
		seconds = defaultSegmentSeconds
	}

	tp := TitlePoint{Point: Point{Height: r.Height, CRF: r.CRF}, Width: r.Width, VMAF: r.VMAF, Bitrate: make([]float64, len(r.Bits))}
	for i, bits := range r.Bits {
		tp.Bitrate[i] = float64(bits) / seconds
	}

	return tp
}

// titleName names a title after its source file.
func titleName(
	source string,
) string {
	return strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
}

// Same tells whether r and other are the same measurement of the same
// excerpt of a title: the same encode, or the same version of its
// features, of as many segments. A title whose excerpt is no longer cut
// the same way is measured again.
func (r Row) Same(
	other Row,
) bool {
	if a, b := r.segments(), other.segments(); a != b && a > 0 && b > 0 {
		return false
	}

	if r.Kind == FeaturesKind {
		// The features of a title are those of its pictures, whatever
		// the encoder.
		return other.Kind == FeaturesKind && r.Source == other.Source && r.Version == other.Version
	}

	return r.Kind == other.Kind && r.Source == other.Source && r.Codec == other.Codec && r.Preset == other.Preset &&
		r.Height == other.Height && r.CRF == other.CRF
}

// segments is the number of segments the row measures: what it says, or
// the length of its measurements; zero when it holds neither.
func (r Row) segments() int {
	if r.Segments > 0 {
		return r.Segments
	}

	for _, values := range r.Features {
		return len(values)
	}

	return len(r.Bits)
}

// ReadRows reads every row of the corpus file at path; a file that does
// not exist has none.
func ReadRows(
	path string,
) ([]Row, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read corpus: %w", err)
	}
	defer file.Close()

	var rows []Row

	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 64<<20)

	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}

		var row Row
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("%s: %w: %w", path, ErrCorpus, err)
		}

		rows = append(rows, row)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w: %w", path, ErrCorpus, err)
	}

	return rows, nil
}
