package decode

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

func TestSumTree(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		terms     int
		wantDepth int
	}{
		{name: "flat", terms: 3, wantDepth: 0},
		{name: "at the limit", terms: maxTerms, wantDepth: 0},
		{name: "one level", terms: maxTerms + 1, wantDepth: 1},
		{name: "two levels", terms: maxTerms*maxTerms + 1, wantDepth: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			terms := make([]string, testCase.terms)
			for i := range terms {
				terms[i] = "t" + strconv.Itoa(i)
			}

			expr := sumTree(terms)

			assert.Equal(t, testCase.terms, strings.Count(expr, "t"))
			assert.Equal(t, testCase.wantDepth, maxDepth(expr))
			assert.LessOrEqual(t, longestChain(expr), maxTerms)
		})
	}
}

func maxDepth(
	expr string,
) int {
	depth, deepest := 0, 0

	for _, r := range expr {
		switch r {
		case '(':
			depth++
			deepest = max(deepest, depth)
		case ')':
			depth--
		}
	}

	return deepest
}

// longestChain returns the largest number of '+'-joined operands at one level.
func longestChain(
	expr string,
) int {
	counts := map[int]int{0: 1}
	depth, longest := 0, 1

	for _, r := range expr {
		switch r {
		case '(':
			depth++
			counts[depth] = 1
		case ')':
			depth--
		case '+':
			counts[depth]++
			longest = max(longest, counts[depth])
		}
	}

	return longest
}

func TestFilters(
	t *testing.T,
) {
	testCases := []struct {
		name string
		req  Request
		want string
	}{
		{
			name: "luma only",
			req:  Request{Pool: frame.NewPool(320, 180, frame.PoolOptions{}), SourceWidth: 320, SourceHeight: 180},
			want: "extractplanes=y,scale=320:180:flags=bicubic,format=gray",
		},
		{
			name: "luma is extracted before scaling",
			req:  Request{Pool: frame.NewPool(160, 90, frame.PoolOptions{}), SourceWidth: 320, SourceHeight: 180},
			want: "extractplanes=y,scale=160:90:flags=bicubic,format=gray",
		},
		{
			name: "high bit depth luma only",
			req: Request{
				Pool:        frame.NewPool(320, 180, frame.PoolOptions{HighBitDepth: true}),
				SourceWidth: 320, SourceHeight: 180,
			},
			want: "extractplanes=y,scale=320:180:flags=bicubic,format=gray10le",
		},
		{
			name: "selection, scaling and chroma",
			req: Request{
				Pool:        frame.NewPool(1920, 1080, frame.PoolOptions{Chroma: true}),
				SourceWidth: 1280, SourceHeight: 720,
				Select: [][2]int{{0, 4}, {10, 12}},
			},
			want: `select='between(n\,0\,3)+between(n\,10\,11)',scale=1920:1080:flags=bicubic,format=yuv420p`,
		},
		{
			name: "high bit depth chroma",
			req: Request{
				Pool:        frame.NewPool(320, 180, frame.PoolOptions{Chroma: true, HighBitDepth: true}),
				SourceWidth: 320, SourceHeight: 180,
			},
			want: "scale=320:180:flags=bicubic,format=yuv420p10le",
		},

		{
			name: "hlg tone mapped with its own tags",
			req: Request{
				Pool:        frame.NewPool(320, 180, frame.PoolOptions{Chroma: true, HighBitDepth: true}),
				SourceWidth: 320, SourceHeight: 180,
				ToneMap: &ToneMap{Input: media.Color{
					Transfer: media.TransferHLG, Primaries: "bt2020", Space: "bt2020nc", Range: "pc",
				}},
			},
			want: "scale=320:180:flags=bicubic:in_transfer=arib-std-b67:in_primaries=bt2020:in_color_matrix=bt2020nc:" +
				"in_range=pc:out_transfer=bt709:out_primaries=bt709:out_color_matrix=bt709:out_range=tv:intent=perceptual," +
				"format=yuv420p10le",
		},
		{
			name: "luma pools are not tone mapped",
			req: Request{
				Pool:        frame.NewPool(320, 180, frame.PoolOptions{}),
				SourceWidth: 320, SourceHeight: 180,
				ToneMap: &ToneMap{Input: media.Color{Transfer: media.TransferPQ}},
			},
			want: "extractplanes=y,scale=320:180:flags=bicubic,format=gray",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, filters(testCase.req))
		})
	}
}

func TestArgs(
	t *testing.T,
) {
	pool := frame.NewPool(320, 180, frame.PoolOptions{})
	ntsc := media.Rational{Num: 30000, Den: 1001}

	testCases := []struct {
		name    string
		threads int
		req     Request
		want    string
	}{
		{
			name: "whole file",
			req:  Request{Path: "in.mp4", Pool: pool, SourceWidth: 320, SourceHeight: 180},
			want: "-v error -nostdin -threads 0 -i in.mp4 -map 0:v:0 -fps_mode passthrough -an -sn -dn " +
				"-vf extractplanes=y,scale=320:180:flags=bicubic,format=gray -f rawvideo -",
		},
		{
			name:    "seek half a frame early with limits and thread override",
			threads: 4,
			req: Request{
				Path: "in.mp4", Pool: pool, SourceWidth: 320, SourceHeight: 180,
				Start: media.Seconds(2), FrameRate: ntsc, MaxFrames: 10, Threads: 2,
			},
			want: "-v error -nostdin -threads 2 -seek_timestamp 1 -ss 1.983317 -i in.mp4 -map 0:v:0 -fps_mode passthrough " +
				"-an -sn -dn -vf extractplanes=y,scale=320:180:flags=bicubic,format=gray -frames:v 10 -f rawvideo -",
		},
		{
			name: "seek on the container's timeline",
			req: Request{
				Path: "in.mp4", Pool: pool, SourceWidth: 320, SourceHeight: 180,
				Start: media.Seconds(2), Origin: media.Seconds(1.4), FrameRate: ntsc,
			},
			want: "-v error -nostdin -threads 0 -seek_timestamp 1 -ss 3.383317 -i in.mp4 -map 0:v:0 -fps_mode passthrough " +
				"-an -sn -dn -vf extractplanes=y,scale=320:180:flags=bicubic,format=gray -f rawvideo -",
		},
		{
			name: "chroma pools keep their filter graph",
			req:  Request{Path: "in.mp4", Pool: frame.NewPool(320, 180, frame.PoolOptions{Chroma: true}), SourceWidth: 320, SourceHeight: 180},
			want: "-v error -nostdin -threads 0 -reinit_filter 0 -i in.mp4 -map 0:v:0 -fps_mode passthrough -an -sn -dn " +
				"-vf scale=320:180:flags=bicubic,format=yuv420p -f rawvideo -",
		},
		{
			name:    "seek never goes before the first frame",
			threads: 3,
			req: Request{
				Path: "in.mp4", Pool: pool, SourceWidth: 320, SourceHeight: 180,
				Start: media.Seconds(0.001), FrameRate: ntsc,
			},
			want: "-v error -nostdin -threads 3 -seek_timestamp 1 -ss 0.000000 -i in.mp4 -map 0:v:0 -fps_mode passthrough " +
				"-an -sn -dn -vf extractplanes=y,scale=320:180:flags=bicubic,format=gray -f rawvideo -",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := NewFFmpeg("ffmpeg", testCase.threads).args(testCase.req, HWAccelNone)

			assert.Equal(t, testCase.want, strings.Join(got, " "))
		})
	}
}

func TestFrameDuration(
	t *testing.T,
) {
	testCases := []struct {
		name string
		rate media.Rational
		want float64
	}{
		{name: "known", rate: media.Rational{Num: 25, Den: 1}, want: 0.04},
		{name: "unknown", rate: media.Rational{}, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, frameDuration(testCase.rate), 1e-12)
		})
	}
}
