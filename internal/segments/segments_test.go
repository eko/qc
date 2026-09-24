package segments

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/media"
)

func TestDetect(
	t *testing.T,
) {
	pts := []media.Duration{0, media.Seconds(1), media.Seconds(2), media.Seconds(3), media.Seconds(4)}
	end := media.Seconds(5)

	testCases := []struct {
		name  string
		flags []bool
		min   media.Duration
		want  []media.Interval
	}{
		{name: "none", flags: []bool{false, false, false, false, false}, min: 0, want: nil},
		{
			name:  "run in the middle",
			flags: []bool{false, true, true, false, false},
			min:   media.Seconds(2),
			want:  []media.Interval{{Start: media.Seconds(1), End: media.Seconds(3)}},
		},
		{
			name:  "run until the end",
			flags: []bool{false, false, false, true, true},
			min:   0,
			want:  []media.Interval{{Start: media.Seconds(3), End: end}},
		},
		{
			name:  "short run filtered",
			flags: []bool{true, false, true, true, false},
			min:   media.Seconds(2),
			want:  []media.Interval{{Start: media.Seconds(2), End: media.Seconds(4)}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Detect(testCase.flags, pts, end, testCase.min))
		})
	}
}
