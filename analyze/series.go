package analyze

import (
	"cmp"
	"slices"
	"sync"
)

// Series gathers the per-frame values of an analyzer measured by runs of
// consecutive frames (its forks, or the single run of a sequential pass)
// and merges them in frame order. The zero value is ready to use and safe
// for concurrent use.
type Series[T any] struct {
	mu   sync.Mutex
	runs []seriesRun[T]
}

// seriesRun holds the values of one run, frame first being values[0].
type seriesRun[T any] struct {
	first  int
	values []T
}

// Add records the values of the frames of a run, the first of them being
// frame first.
func (s *Series[T]) Add(
	first int,
	values []T,
) {
	if len(values) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.runs = append(s.runs, seriesRun[T]{first: first, values: values})
}

// Merge returns the values of every frame in order, the first value being
// that of the earliest frame measured. Where runs overlap, the earlier
// run's values are kept: that run also had the frame's predecessor (see
// Forker). Runs are expected to leave no gap (RunSegments checks it).
func (s *Series[T]) Merge() []T {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.runs) == 1 {
		return s.runs[0].values
	}

	slices.SortFunc(s.runs, func(a, b seriesRun[T]) int { return cmp.Compare(a.first, b.first) })

	var out []T

	for _, run := range s.runs {
		start := 0
		if len(out) > 0 {
			start = min(max(s.runs[0].first+len(out)-run.first, 0), len(run.values))
		}

		out = append(out, run.values[start:]...)
	}

	return out
}
