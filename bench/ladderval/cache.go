package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
)

// ErrCacheMismatch is returned when a measurement cache was recorded for
// another source, codec, preset or precision: reusing it would compare
// ladders against the wrong ground truth.
var ErrCacheMismatch = errors.New("measurement cache recorded with other settings")

// cacheFile is the on-disk measurement cache. The exhaustive grid is the
// expensive part of a validation (35 full-title encodes with exact VMAF for
// a 1080p title) and does not depend on the ladder being checked, so
// comparing several ladders of one title (fixed vs adaptive probing, per-shot
// vs per-title) only pays for it once.
type cacheFile struct {
	Source    string                  `json:"source"`
	Codec     string                  `json:"codec"`
	Preset    string                  `json:"preset"`
	Precision float64                 `json:"precision"`
	Entries   map[string]ladder.Probe `json:"entries"`
}

// cacheKey identifies an encode by every setting that changes its result.
func cacheKey(
	p encode.Params,
) string {
	return fmt.Sprintf("%dx%d/crf%s/max%d/buf%d/depth%d",
		p.Width, p.Height, strconv.FormatFloat(p.CRF, 'f', -1, 64), p.MaxRate, p.BufSize, p.BitDepth)
}

// cached wraps measure with the cache stored at path (created when missing).
// Every new measurement is written back immediately, so an interrupted
// validation resumes where it stopped.
func cached(
	path string,
	header cacheFile,
	measure measureFunc,
) (measureFunc, error) {
	cache, err := loadCache(path, header)
	if err != nil {
		return nil, err
	}

	return func(p encode.Params) (ladder.Probe, error) {
		key := cacheKey(p)
		if got, ok := cache.Entries[key]; ok {
			return got, nil
		}

		got, err := measure(p)
		if err != nil {
			return ladder.Probe{}, err
		}

		cache.Entries[key] = got

		if err := saveCache(path, cache); err != nil {
			return ladder.Probe{}, err
		}

		return got, nil
	}, nil
}

// loadCache reads the cache at path, or starts an empty one with header.
func loadCache(
	path string,
	header cacheFile,
) (cacheFile, error) {
	header.Entries = map[string]ladder.Probe{}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return header, nil
	}

	if err != nil {
		return cacheFile{}, fmt.Errorf("read cache: %w", err)
	}

	var cache cacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return cacheFile{}, fmt.Errorf("decode cache %s: %w", path, err)
	}

	if cache.Source != header.Source || cache.Codec != header.Codec || cache.Preset != header.Preset || cache.Precision != header.Precision {
		return cacheFile{}, fmt.Errorf("%s: %w (%s %s %s ±%g)", path, ErrCacheMismatch, cache.Source, cache.Codec, cache.Preset, cache.Precision)
	}

	if cache.Entries == nil {
		cache.Entries = map[string]ladder.Probe{}
	}

	return cache, nil
}

// saveCache writes cache to path.
func saveCache(
	path string,
	cache cacheFile,
) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}

	return nil
}
