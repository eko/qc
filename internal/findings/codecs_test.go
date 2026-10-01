package findings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
)

// codecLadder is a verified ladder of codec whose rungs are (bitrate in
// kb/s, VMAF) pairs, top first.
func codecLadder(
	codec string,
	pairs ...float64,
) *ladder.Result {
	r := &ladder.Result{Codec: encode.Codec{Name: codec}}

	for i := 0; i < len(pairs); i += 2 {
		r.Rungs = append(r.Rungs, ladder.Rung{
			Measured: &ladder.Measurement{Bitrate: int64(pairs[i] * 1000), VMAF: pairs[i+1]},
		})
	}

	return r
}

func TestCodecs(
	t *testing.T,
) {
	h264 := codecLadder("h264", 8000, 93, 4000, 87, 2000, 81)
	lighter := codecLadder("av1", 5600, 93, 2800, 87, 1400, 81)
	// 7% more at the top, the same below.
	costlierTop := codecLadder("av1", 8560, 93, 4000, 87, 2000, 81)
	// The same at the top, 10% more below: 5% more on average.
	costlierMean := codecLadder("hevc", 8000, 93, 4400, 87, 2200, 81)
	// 2% more: within what the rungs can tell.
	level := codecLadder("av1", 8160, 93, 4080, 87, 2040, 81)

	testCases := []struct {
		name    string
		ladders []*ladder.Result
		want    []Finding
	}{
		{name: "a single codec", ladders: []*ladder.Result{h264}},
		{name: "no ladder"},
		{
			name:    "a newer codec saving bitrate",
			ladders: []*ladder.Result{h264, lighter},
			want:    []Finding{{Level: Info, Code: CodecSaving, Index: 1, Other: 0, Value: -0.3, Limit: 93}},
		},
		{
			name:    "the oldest codec is the reference wherever it comes",
			ladders: []*ladder.Result{lighter, h264},
			want:    []Finding{{Level: Info, Code: CodecSaving, Index: 0, Other: 1, Value: -0.3, Limit: 93}},
		},
		{
			name:    "a newer codec costlier at the top quality",
			ladders: []*ladder.Result{h264, costlierTop},
			want:    []Finding{{Level: Warn, Code: CodecCostlier, Index: 1, Other: 0, Value: 0.07, Limit: 93}},
		},
		{
			name:    "a newer codec costlier on average",
			ladders: []*ladder.Result{h264, costlierMean},
			want:    []Finding{{Level: Warn, Code: CodecCostlier, Index: 1, Other: 0, Value: 0, Limit: 93}},
		},
		{
			name:    "a gap within the tolerance is a saving of nothing",
			ladders: []*ladder.Result{h264, level},
			want:    []Finding{{Level: Info, Code: CodecSaving, Index: 1, Other: 0, Value: 0.02, Limit: 93}},
		},
		{
			name:    "every newer codec against the oldest",
			ladders: []*ladder.Result{h264, costlierMean, lighter},
			want: []Finding{
				{Level: Warn, Code: CodecCostlier, Index: 1, Other: 0, Value: 0, Limit: 93},
				{Level: Info, Code: CodecSaving, Index: 2, Other: 0, Value: -0.3, Limit: 93},
			},
		},
		{
			name:    "two ladders of one codec are not compared",
			ladders: []*ladder.Result{h264, h264},
		},
		{
			name:    "an unknown codec is not compared",
			ladders: []*ladder.Result{h264, codecLadder("vvc", 4000, 93, 2000, 87)},
		},
		{
			name:    "ladders sharing no quality are not compared",
			ladders: []*ladder.Result{h264, codecLadder("av1", 500, 60, 250, 50)},
		},
		{
			name:    "a ladder without rungs is not compared",
			ladders: []*ladder.Result{h264, codecLadder("av1")},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := Codecs(testCase.ladders)
			require.Len(t, got, len(testCase.want))

			for i, want := range testCase.want {
				assert.Equal(t, want.Level, got[i].Level)
				assert.Equal(t, want.Code, got[i].Code)
				assert.Equal(t, want.Index, got[i].Index)
				assert.Equal(t, want.Other, got[i].Other)
				assert.InDelta(t, want.Value, got[i].Value, 1e-6)
				assert.InDelta(t, want.Limit, got[i].Limit, 1e-9)
			}
		})
	}
}

func TestLadderTopDigest(
	t *testing.T,
) {
	r := verified()
	r.Digest = ladder.Digest{Sampling: ladder.DigestTop, Complexity: &ladder.DigestComplexity{TitleTI: 13.2, TI: 36.7}}

	assert.Contains(t, Ladder(r), Finding{Level: Info, Code: TopDigest, Value: 36.7, Limit: 13.2})

	r.Digest.Sampling = ladder.DigestBalanced
	assert.NotContains(t, codes(Ladder(r)), [2]string{"info", string(TopDigest)})
}
