package svg

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// wellFormed checks that the chart is valid XML, as inline SVG must be.
func wellFormed(
	t *testing.T,
	svg string,
) {
	t.Helper()

	dec := xml.NewDecoder(strings.NewReader(svg))

	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return
		}

		require.NoError(t, err, svg)
	}
}

var chartScripts = regexp.MustCompile(`<script type="application/(?:json|octet-stream)" class="chart-data"( data-encoding="gzip")?>([^<]*)</script>`)

// payloads decodes the data of every chart of a page, as the page script
// does.
func payloads(
	t *testing.T,
	html string,
) []chartData {
	t.Helper()

	var out []chartData

	for _, m := range chartScripts.FindAllStringSubmatch(html, -1) {
		raw := []byte(m[2])

		if m[1] != "" {
			packed, err := base64.StdEncoding.DecodeString(m[2])
			require.NoError(t, err)

			zr, err := gzip.NewReader(bytes.NewReader(packed))
			require.NoError(t, err)

			raw, err = io.ReadAll(zr)
			require.NoError(t, err)
		}

		var d chartData
		require.NoError(t, json.Unmarshal(raw, &d), string(raw))
		out = append(out, d)
	}

	return out
}

// values decodes a column, as the page script does.
func (c column) values() []float64 {
	out := make([]float64, c.N)

	var acc int64

	for i := range out {
		if c.Deltas != nil {
			acc += c.Deltas[i]
			out[i] = float64(acc) / c.Scale

			continue
		}

		out[i] = c.Start + float64(i)*c.Step
	}

	return out
}

// seriesNamed is the series of a chart with that name.
func seriesNamed(
	t *testing.T,
	d chartData,
	name string,
) seriesData {
	t.Helper()

	for _, s := range d.Series {
		if s.Name == name {
			return s
		}
	}

	require.Failf(t, "no such series", "%q", name)

	return seriesData{}
}
