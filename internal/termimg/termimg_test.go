package termimg

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		env     map[string]string
		want    Protocol
		wantErr error
	}{
		{name: "iTerm2", env: map[string]string{"TERM_PROGRAM": "iTerm.app"}, want: ITerm},
		{name: "iTerm2 over ssh", env: map[string]string{"LC_TERMINAL": "iTerm2"}, want: ITerm},
		{name: "WezTerm", env: map[string]string{"TERM_PROGRAM": "WezTerm"}, want: ITerm},
		{name: "Warp", env: map[string]string{"TERM_PROGRAM": "WarpTerminal"}, want: Kitty},
		{name: "Ghostty", env: map[string]string{"TERM_PROGRAM": "ghostty"}, want: Kitty},
		{name: "Kitty by its TERM", env: map[string]string{"TERM": "xterm-kitty"}, want: Kitty},
		{name: "Kitty by its window", env: map[string]string{"KITTY_WINDOW_ID": "1"}, want: Kitty},
		{name: "Apple Terminal", env: map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, want: None},
		{name: "inside tmux", env: map[string]string{"TERM_PROGRAM": "iTerm.app", "TMUX": "/tmp/tmux"}, want: None},
		{name: "forced to text", env: map[string]string{"TERM_PROGRAM": "iTerm.app", ChartsVariable: "text"}, want: None},
		{name: "forced to kitty", env: map[string]string{ChartsVariable: "Kitty"}, want: Kitty},
		{name: "forced inside tmux", env: map[string]string{"TMUX": "/tmp/tmux", ChartsVariable: "iterm"}, want: ITerm},
		{name: "auto", env: map[string]string{"TERM_PROGRAM": "WezTerm", ChartsVariable: "auto"}, want: ITerm},
		{name: "unknown setting", env: map[string]string{ChartsVariable: "sixel"}, wantErr: ErrProtocol},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := Detect(func(name string) string { return testCase.env[name] })
			if testCase.wantErr != nil {
				require.True(t, errors.Is(err, testCase.wantErr), err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestWrite(
	t *testing.T,
) {
	small := []byte("png")
	large := []byte(strings.Repeat("x", 5000))

	testCases := []struct {
		name     string
		png      []byte
		protocol Protocol
		want     []string
		escapes  int
	}{
		{
			name: "iTerm2", png: small, protocol: ITerm, escapes: 1,
			want: []string{"\x1b]1337;File=inline=1;size=3;width=80;preserveAspectRatio=1:" + base64.StdEncoding.EncodeToString(small) + "\a\n"},
		},
		{
			name: "Kitty, one chunk", png: small, protocol: Kitty, escapes: 1,
			want: []string{"\x1b_Ga=T,f=100,q=2,c=80,m=0;" + base64.StdEncoding.EncodeToString(small) + "\x1b\\\n"},
		},
		{name: "Kitty, in chunks", png: large, protocol: Kitty, escapes: 2, want: []string{",m=1;", "\x1b_Gm=0;"}},
		{name: "none", png: small, protocol: None},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out strings.Builder
			require.NoError(t, Write(&out, testCase.png, 80, testCase.protocol))

			if testCase.escapes == 0 {
				assert.Empty(t, out.String())

				return
			}

			for _, want := range testCase.want {
				assert.Contains(t, out.String(), want)
			}

			assert.Equal(t, testCase.escapes, strings.Count(out.String(), "\x1b]")+strings.Count(out.String(), "\x1b_"))
		})
	}
}

func TestPlace(
	t *testing.T,
) {
	small := Place([]byte("png"), 80, 14, 7)
	assert.Equal(t, "\x1b_Ga=T,f=100,q=2,C=1,i=7,p=1,c=80,r=14,m=0;"+base64.StdEncoding.EncodeToString([]byte("png"))+"\x1b\\", small)

	large := Place([]byte(strings.Repeat("x", 5000)), 80, 14, 7)
	assert.Equal(t, 1, strings.Count(large, "a=T"))
	assert.Equal(t, 2, strings.Count(large, "\x1b_G"))
	assert.True(t, strings.HasSuffix(large, "\x1b\\"))
	assert.Contains(t, large, "\x1b_Gm=0;")

	assert.Equal(t, "\x1b_Ga=d,d=I,i=7,q=2\x1b\\", Delete(7))
}
