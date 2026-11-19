package modeldiagnostics

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlansAndCommunityQuestions(t *testing.T) {
	p, err := NewPlan("intelligence", false)
	require.NoError(t, err)
	require.Len(t, p.Steps, 18)
	// Preserve the fixed upstream's original weights, whose true total is 35.
	weight := 0
	for _, p := range p.Steps {
		weight += p.Weight
	}
	assert.Equal(t, 35, weight)
	for _, q := range intelligenceQuestions {
		if q.ID == "code-function" {
			ok, _ := gradeIntelligence(q, "function sumEven(n) { return 7 }")
			assert.Nil(t, ok)
			continue
		}
		ok, _ := gradeIntelligence(q, q.Expected)
		require.NotNil(t, ok, q.ID)
		assert.True(t, *ok, q.ID)
	}
	q := intelligenceQuestions[17]
	ok, _ := gradeIntelligence(q, "不一样重")
	require.NotNil(t, ok)
	assert.False(t, *ok)
	var answers []Answer
	for _, q := range intelligenceQuestions {
		answers = append(answers, Answer{ID: q.ID, Text: q.Expected, Complete: true})
	}
	answers[0].Complete = false
	r := Evaluate("intelligence", answers)
	assert.Equal(t, "incomplete", r.Status)
	assert.Nil(t, r.Score)
	assert.Nil(t, r.Passed)
	d := r.Details.(IntelligenceDetails)
	assert.Equal(t, 17, d.Completed)
	assert.Equal(t, 35, d.WeightedMax)
	answers[0].Complete = true
	r = Evaluate("intelligence", answers)
	assert.Equal(t, "unknown", r.Status)
	assert.Nil(t, r.Score)
	_, err = NewPlan("unsupported", false)
	require.Error(t, err)
	_, err = NewPlan("connection", true)
	require.Error(t, err)
	p, err = NewPlan("drawing", true)
	require.NoError(t, err)
	assert.Equal(t, "mowan-adapted-v1", p.Materials["pool"])
	for key, value := range p.Materials {
		if key != "pool" {
			assert.Contains(t, p.Steps[0].Prompt, value)
		}
	}
}

func TestQuestionsMatchPinnedUpstream(t *testing.T) {
	source, err := os.ReadFile("data/intelligence.js")
	require.NoError(t, err)
	re := regexp.MustCompile(`\{ id: '([^']+)', dim: '([^']+)', weight: (\d+), maxTokens: (\d+), prompt: '([^']+)', expected: '([^']+)' \}`)
	matches := re.FindAllStringSubmatch(string(source), -1)
	require.Len(t, matches, 18)
	for i, m := range matches {
		q := intelligenceQuestions[i]
		weight, _ := strconv.Atoi(m[3])
		tokens, _ := strconv.Atoi(m[4])
		assert.Equal(t, m[1], q.ID)
		assert.Equal(t, m[2], q.Title)
		assert.Equal(t, weight, q.Weight)
		assert.Equal(t, tokens, q.MaxTokens)
		assert.Equal(t, m[5], q.Prompt)
		assert.Equal(t, strings.ReplaceAll(m[6], `\n`, "\n"), q.Expected)
	}
}

func TestIntelligenceStaticCodeProof(t *testing.T) {
	q := intelligenceQuestions[10]
	loop := `function sumEven(n) { let sum = 0; for (let i = 2; i <= n; i += 2) { sum += i; } return sum; }`
	formula := `function sumEven(n) { if (n < 2) return 0; const k = Math.floor(n / 2); return k * (k + 1); }`
	for _, code := range []string{
		loop,
		strings.ReplaceAll(loop, "let", "var"),
		strings.Replace(loop, "{ sum += i; }", "sum += i;", 1),
		strings.ReplaceAll(strings.ReplaceAll(loop, "sum", "total"), "totalEven", "sumEven"),
		formula,
		strings.Replace(formula, "if (n < 2) return 0;", "if (n < 2) { return 0; }", 1),
		"```javascript\n" + loop + "\n```",
	} {
		ok, reason := gradeIntelligence(q, code)
		require.NotNil(t, ok, code)
		assert.True(t, *ok, reason)
	}
	for _, code := range []string{
		"```\n```",
		"```javascript\n```",
		"```javascript\n" + loop,
		strings.Repeat(" ", 16*1024) + loop,
		`function sumEven(n) { return n; }`,
		strings.Replace(loop, "sum += i", "sum += n", 1),
		strings.Replace(loop, "i += 2", "i += 1", 1),
		strings.Replace(loop, "i <= n", "i < n", 1),
		strings.Replace(loop, "let i", "const i", 1),
		strings.ReplaceAll(loop, " i", " n"),
		strings.Replace(loop, "return sum", "return\nsum", 1),
		strings.Replace(loop, "let sum", "let class", 1),
		strings.ReplaceAll(strings.ReplaceAll(loop, "sum", "class"), "classEven", "sumEven"),
		strings.ReplaceAll(loop, " i", " sum"),
		strings.ReplaceAll(formula, "k", "Math"),
		strings.Replace(loop, "let sum", "letsum", 1),
		strings.Replace(loop, "sum += i;", "sum += i; evil();", 1),
		strings.Replace(loop, "sum += i;", `sum += i; "harmless";`, 1),
		strings.Replace(loop, "sum += i;", "sum += i; /* ignored? */", 1),
		strings.Replace(formula, "if (n < 2) return 0; ", "", 1),
		strings.Replace(formula, "Math.floor", "Math.ceil", 1),
		loop + `; sendSecrets();`,
		loop + "\nANSWER: " + loop,
		loop + "\u2028evil()",
	} {
		ok, _ := gradeIntelligence(q, code)
		assert.True(t, ok == nil || !*ok, code)
	}
	var answers []Answer
	for _, q := range intelligenceQuestions {
		answers = append(answers, Answer{ID: q.ID, Text: q.Expected, Complete: true})
	}
	answers[10].Text = loop
	r := Evaluate("intelligence", answers)
	require.NotNil(t, r.Score)
	assert.Equal(t, 100, *r.Score)
	assert.True(t, r.Complete)
	assert.True(t, *r.Passed)
	answers[7].Text = "170"
	r = Evaluate("intelligence", answers)
	require.NotNil(t, r.Score)
	assert.Equal(t, 91, *r.Score)
}

func TestFrozenFingerprintOffline(t *testing.T) {
	d, err := loadDetector()
	require.NoError(t, err)
	assert.NotEmpty(t, d.BuiltAt())
	_, err = verifiedDetector([]byte("changed"), detectorBytes)
	require.ErrorContains(t, err, "SHA-256 mismatch")
	p, err := NewPlan("fingerprint", false)
	require.NoError(t, err)
	require.Len(t, p.Steps, 3)
	counts := map[int]bool{}
	for _, s := range p.Steps {
		assert.GreaterOrEqual(t, s.ExpectedCount, 292)
		assert.LessOrEqual(t, s.ExpectedCount, 332)
		assert.False(t, counts[s.ExpectedCount])
		counts[s.ExpectedCount] = true
	}
	a := []Answer{{ID: "a", Text: "1 2 3", Complete: true, Source: "private-channel", ExpectedCount: 300}}
	r := Evaluate("fingerprint", a)
	assert.False(t, r.Complete)
	assert.Nil(t, r.Score)
	encoded, err := common.Marshal(r)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private-channel")
	a = append(a, Answer{ID: "b", Text: "1 2 3", Complete: true, Source: "other", ExpectedCount: 301})
	r = Evaluate("fingerprint", a)
	assert.Equal(t, "unknown", r.Status)
	assert.Nil(t, r.Details.(FingerprintDetails).Analysis)
	// Synthetic numeric fixture exercises the SDK's full local scoring path;
	// it is deliberately not represented as a real model sample or accuracy test.
	a = nil
	for i := 0; i < 3; i++ {
		a = append(a, Answer{ID: fmt.Sprintf("sample-%d", i), Text: strings.Repeat("1 355 ", 150), Complete: true, Source: "same", ExpectedCount: 300})
	}
	r = Evaluate("fingerprint", a)
	require.Equal(t, "completed", r.Status)
	require.True(t, r.Complete)
	fd := r.Details.(FingerprintDetails)
	require.NotNil(t, fd.Analysis)
	assert.Equal(t, 3, fd.ValidSamples)
	assert.NotEmpty(t, fd.Analysis.Results)
	r = Evaluate("fingerprint", a[:2])
	assert.Equal(t, "incomplete", r.Status)
	assert.Empty(t, r.Details.(FingerprintDetails).Analysis.Results)
}

func svgFixture(padding int) string {
	var shapes strings.Builder
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&shapes, `<path d="M0 0 L10 10" stroke="%s"/>`, []string{"red", "blue", "green", "yellow"}[i%4])
	}
	return `<!doctype html><html><body><svg viewBox="0 0 100 100"><style>@keyframes spin {to {transform:rotate(360deg)}} path{animation:spin 2s linear infinite}</style>` + shapes.String() + `</svg><!--` + strings.Repeat("x", padding) + `--></body></html>`
}
func drawing(raw string) Result {
	return Evaluate("drawing", []Answer{{ID: "drawing", Text: raw, Complete: true}})
}

func TestDrawingScoreBoundariesAndSingleAnomaly(t *testing.T) {
	base := svgFixture(0)
	for _, n := range []int{1499, 1500, 4095, 4096} {
		r := drawing(svgFixture(n - len(base)))
		require.NotNil(t, r.Score)
		d := r.Details.(DrawingDetails)
		assert.Equal(t, n, d.Bytes)
		want := 95
		if n >= 1500 {
			want = 100
		}
		if n >= 4096 {
			assert.Equal(t, 105, d.StructuralPoints)
		}
		assert.Equal(t, want, *r.Score)
	}
	raw := svgFixture(4096-len(base)) + `<!-- TODO: implement missing animation -->`
	r := drawing(raw)
	require.NotNil(t, r.Score)
	d := r.Details.(DrawingDetails)
	assert.Equal(t, 105, d.StructuralPoints)
	assert.Equal(t, 30, d.AnomalyPenalty)
	assert.Equal(t, 20, d.StructuralPenalty)
	assert.Equal(t, 55, *r.Score)
	assert.False(t, *r.Passed)
	d = analyzeDrawing(`<html><svg><text>待补充 SVG</text></svg></html>`)
	assert.Greater(t, len(d.AnomalyReasons), 1)
	assert.Equal(t, 30, d.AnomalyPenalty)
	// A 105-point artifact with a confirmed anomaly is capped after subtracting 30.
	assert.Equal(t, 75, max(0, min(100, 105-30)))
}

func TestDrawingDefinitionsColorsAndUnknown(t *testing.T) {
	r := drawing(`<svg viewBox="0 0 10 10"><defs><path id="x" d="M0 0"/><circle r="2"/></defs><path d="M0 0 L1 1" fill="red" stroke="#ff0000"/><path d="M1 1 L2 2" fill="rgb(255, 0, 0)"/><use href="#x" fill="hsl(0 100% 50%)"/></svg>`)
	d := r.Details.(DrawingDetails)
	require.NotNil(t, d.Elements)
	assert.Equal(t, 3, *d.Elements)
	assert.Equal(t, 2, *d.Paths)
	assert.Nil(t, d.Colors, "referenced paint is not statically expanded")
	assert.Nil(t, r.Score)
	for _, v := range []string{"red", "#f00", "#ff0000", "rgb(255,0,0)", "rgb(100% 0% 0%)", "hsl(0 100% 50%)"} {
		color, ok := normalizeColor(v)
		assert.True(t, ok, v)
		assert.Equal(t, "ff0000ff", color, v)
	}
	for _, v := range []string{"transparent", "none", "#ff000000", "rgba(255,0,0,0)"} {
		color, ok := normalizeColor(v)
		assert.True(t, ok)
		assert.Empty(t, color)
	}
	r = drawing(`<svg viewBox="0 0 10 10"><path d="M0 0L1 1" fill="var(--color)"/></svg>`)
	assert.Equal(t, "unknown", r.Status)
	assert.Nil(t, r.Score)
	assert.Nil(t, r.Details.(DrawingDetails).Colors)
	r = drawing(svgFixture(5000) + `<script>requestAnimationFrame(draw)</script>`)
	assert.Equal(t, "unknown", r.Status)
	assert.Nil(t, r.Score)
	d = r.Details.(DrawingDetails)
	assert.NotContains(t, d.AnomalyReasons, "no_loop_animation")
	assert.Equal(t, 0, d.AnomalyPenalty)
	r = Evaluate("drawing", []Answer{{ID: "drawing", Text: svgFixture(0), Complete: false}})
	assert.Equal(t, "incomplete", r.Status)
	assert.Nil(t, r.Score)
}

func TestDrawingLoopAndPlaceholders(t *testing.T) {
	d := analyzeDrawing(`<svg viewBox="0 0 10 10"><circle r="2"><animate attributeName="cx" values="0;1" dur="2s" repeatCount="indefinite"/></circle><text>星空...</text></svg>`)
	require.NotNil(t, d.Loop)
	assert.True(t, *d.Loop)
	assert.False(t, d.Placeholder)
	d = analyzeDrawing(`<svg><circle r="2"><animate attributeName="cx" values="0;1" dur="0s" repeatCount="indefinite"/></circle></svg>`)
	assert.False(t, *d.Loop)
	d = analyzeDrawing(`<svg><circle r="2"><animate attributeName="cx" values="0;1" dur="2s" repeatCount="1"/></circle></svg>`)
	assert.NotContains(t, d.AnomalyReasons, "no_loop_animation", "finite animation exists; missing loop points alone is not the confirmed no-animation anomaly")
	d = analyzeDrawing(`<svg><style>@keyframes spin{to{transform:rotate(1turn)}}circle{animation:spin 0s 2s infinite}</style><circle r="2"/></svg>`)
	assert.False(t, *d.Loop, "positive delay must not replace zero duration")
	d = analyzeDrawing(`<svg><circle r="2"/>`)
	assert.True(t, d.Unclosed)
	assert.Equal(t, 30, d.AnomalyPenalty)
	d = analyzeDrawing(`<svg><text>...</text><text>TODO list</text></svg>`)
	assert.False(t, d.Placeholder)
}

func TestCSSSpecificityAndActualColors(t *testing.T) {
	d := analyzeDrawing(`<svg><style>.red{fill:red}path{fill:blue}</style><path class="red" d="M0 0L1 1"/><circle fill="#f00" r="2"/></svg>`)
	require.NotNil(t, d.Colors)
	assert.Equal(t, 1, *d.Colors, "class selector wins over element selector")
}

func TestDrawingCSSAnimationFunctionsKeepLoopScore(t *testing.T) {
	short := `<svg viewBox="0 0 100 100"><style>@keyframes spin{to{transform:rotate(1turn)}}path{animation:spin 2s linear infinite}</style>` +
		strings.Repeat(`<path d="M0 0L10 10" stroke="red"/>`, 3) +
		strings.Repeat(`<path d="M0 0L10 10" stroke="blue"/>`, 3) + `</svg>`
	for _, timing := range []string{"linear", "steps(2, end)", "cubic-bezier(0.1, 0.2, 0.3, 0.4)", "linear(0, 0.5 50%, 1)"} {
		t.Run(timing, func(t *testing.T) {
			for _, fixture := range []struct {
				html  string
				score int
			}{{svgFixture(5000), 100}, {short, 85}} {
				r := drawing(strings.ReplaceAll(fixture.html, "linear", timing))
				d := r.Details.(DrawingDetails)
				require.NotNil(t, d.Loop)
				assert.True(t, *d.Loop)
				require.NotNil(t, r.Score)
				assert.Equal(t, fixture.score, *r.Score)
				assert.True(t, *r.Passed)
				assert.Empty(t, d.UnknownMetrics)
			}
		})
	}
}

func TestDrawingCSSAnimationListsQuotesAndUnknown(t *testing.T) {
	for _, change := range []struct{ old, new string }{
		{"spin 2s linear infinite", "absent 0s linear 1, spin 2s steps(2, end) infinite"},
		{"spin", `"s,pin"`},
		{"spin", `"spin wind"`},
	} {
		r := drawing(strings.ReplaceAll(svgFixture(5000), change.old, change.new))
		require.NotNil(t, r.Score, change.new)
		assert.Equal(t, 100, *r.Score, change.new)
		assert.True(t, *r.Passed, change.new)
	}
	for _, value := range []string{
		"spin 2s steps(2, end infinite",
		"spin 2s steps(2, end)) infinite",
		`"spin 2s infinite`,
		"spin 2s infinite,,spin 2s infinite",
	} {
		r := drawing(strings.ReplaceAll(svgFixture(5000), "spin 2s linear infinite", value))
		assert.Equal(t, "unknown", r.Status, value)
		assert.Nil(t, r.Score, value)
		assert.Nil(t, r.Passed, value)
		assert.Contains(t, r.Details.(DrawingDetails).UnknownMetrics, "loop", value)
	}
}
