package modeldiagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The published breakdown must be produced by the same branches as the totals,
// so a reader can add the rows and reach the stored score.
func TestDrawingMetricsMatchTotals(t *testing.T) {
	cases := []string{
		svgFixture(0),
		svgFixture(5000),
		svgFixture(100) + `<!-- TODO: implement missing animation -->`,
		`<html><svg><text>待补充 SVG</text></svg></html>`,
	}
	for _, raw := range cases {
		d := analyzeDrawing(raw)
		awards, penalties := 0, 0
		for _, m := range d.Metrics {
			if !m.Known {
				continue
			}
			if m.Penalty {
				penalties += m.Points
				continue
			}
			awards += m.Points
		}
		assert.Equal(t, d.StructuralPoints, awards)
		assert.Equal(t, d.StructuralPenalty+d.AnomalyPenalty, penalties)
	}
}

func TestDrawingMetricsKeepUnknownUnscored(t *testing.T) {
	d := analyzeDrawing(`<svg><script>document.title="x"</script><circle r="2"/></svg>`)
	require.NotEmpty(t, d.UnknownMetrics)
	byKey := map[string]DrawingMetric{}
	for _, m := range d.Metrics {
		byKey[m.Key] = m
	}
	for _, key := range []string{"valid_svg", "loop", "elements", "paths", "colors", "view_box"} {
		assert.False(t, byKey[key].Known, key)
		assert.Zero(t, byKey[key].Points, key)
	}
	assert.True(t, byKey["bytes"].Known)
	assert.NotEmpty(t, d.Metrics, "unknown drawings still publish what is measurable")
}
