package htmlreport

import (
	"bytes"
	"encoding/base64"
	"html"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrandAssets(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		embedded []byte
		uri      string
		source   string
	}{
		{name: "header logo", embedded: logoSVG, uri: string(logoURI), source: "../../docs/assets/logo.svg"},
		{name: "favicon tile", embedded: logoTileSVG, uri: string(iconURI), source: "../../docs/assets/logo-tile.svg"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// The embedded copy must follow the published logo.
			source, err := os.ReadFile(testCase.source)
			require.NoError(t, err)
			assert.Equal(t, source, testCase.embedded)

			encoded, ok := strings.CutPrefix(testCase.uri, "data:image/svg+xml;base64,")
			require.True(t, ok)

			decoded, err := base64.StdEncoding.DecodeString(encoded)
			require.NoError(t, err)
			assert.Equal(t, testCase.embedded, decoded)
			assert.NotContains(t, string(decoded), "href", "the logo loads nothing")
		})
	}
}

func TestBrandInPage(
	t *testing.T,
) {
	var buf bytes.Buffer
	require.NoError(t, renderResult(&buf, sampleDecodedReport()))

	// html/template writes the + of base64 as &#43; in attributes.
	page := html.UnescapeString(buf.String())
	assert.Contains(t, page, `<link rel="icon" type="image/svg+xml" href="`+string(iconURI)+`">`)
	assert.Contains(t, page, `<img class="logo" src="`+string(logoURI)+`" alt="qc"`)
	assert.Contains(t, page, `<a class="project" href="`+projectURL+`" rel="noopener">qc · github.com/eko/qc</a>`)
}
