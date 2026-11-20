package modeldiagnostics

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/image/colornames"
	"golang.org/x/net/html"
)

const DrawingVersion = "mowan-svg-structure-v1-parser-v2"

type DrawingDetails struct {
	HTML              string          `json:"html"`
	ValidSVG          *bool           `json:"valid_svg"`
	Loop              *bool           `json:"loop"`
	Elements          *int            `json:"elements"`
	Paths             *int            `json:"paths"`
	Colors            *int            `json:"colors"`
	Bytes             int             `json:"bytes"`
	ViewBox           *bool           `json:"view_box"`
	External          bool            `json:"external"`
	Placeholder       bool            `json:"placeholder"`
	Unclosed          bool            `json:"unclosed"`
	StructuralPoints  int             `json:"structural_points"`
	StructuralPenalty int             `json:"structural_penalty"`
	AnomalyPenalty    int             `json:"anomaly_penalty"`
	Anomaly           string          `json:"anomaly"`
	AnomalyReasons    []string        `json:"anomaly_reasons"`
	UnknownMetrics    []string        `json:"unknown_metrics"`
	Metrics           []DrawingMetric `json:"metrics"`
	Limitation        string          `json:"limitation"`
}

// DrawingMetric is one item of the published score breakdown. It is produced by
// the same branches that add to StructuralPoints/penalties, so the displayed
// rows always add up to the stored totals.
type DrawingMetric struct {
	Key     string `json:"key"`
	Value   string `json:"value,omitempty"`
	Points  int    `json:"points"`
	Max     int    `json:"max,omitempty"`
	Penalty bool   `json:"penalty,omitempty"`
	Known   bool   `json:"known"`
}

func evaluateDrawing(answers []Answer) Result {
	r := Result{Status: "incomplete", Version: DrawingVersion, Reasons: []string{}}
	if len(answers) != 1 || answers[0].ID != "drawing" || !answers[0].Complete {
		r.Reasons = []string{"generation_not_completed"}
		return r
	}
	d := analyzeDrawing(answers[0].Text)
	r.Details = d
	if len(d.UnknownMetrics) > 0 {
		r.Status = "unknown"
		r.Reasons = []string{"static_metrics_unknown"}
		return r
	}
	score := max(0, min(100, d.StructuralPoints-d.StructuralPenalty-d.AnomalyPenalty))
	r.Score = &score
	r.Complete = true
	r.Status = "completed"
	r.Passed = pointer(score >= 70 && d.Anomaly == "not_detected")
	r.Reasons = d.AnomalyReasons
	return r
}

var placeholderRE = regexp.MustCompile(`(?i)(?:TODO|FIXME)[^\n<>]{0,100}(?:implement|unfinished|add\b|complete|未实现|待补充|待完成|添加|补充)|(?:在此添加|待补充|尚未实现|未实现)[^\n<>]{0,30}(?:SVG|动画)|(?:SVG|动画)[^\n<>]{0,20}(?:待补充|尚未实现)`)
var cssCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
var urlRE = regexp.MustCompile(`(?i)url\(\s*['"]?([^\s'"\)]+)`)
var durationRE = regexp.MustCompile(`^([+]?(?:\d+(?:\.\d*)?|\.\d+))(ms|s|min|h)?$`)

func analyzeDrawing(raw string) DrawingDetails {
	d := DrawingDetails{HTML: raw, Bytes: len([]byte(raw)), AnomalyReasons: []string{}, UnknownMetrics: []string{}, Limitation: "本地静态结构规则，不验证题意、美学、真实动画效果或模型身份；JavaScript 动态内容无法完整静态评分。"}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		d.UnknownMetrics = append(d.UnknownMetrics, "parse")
		d.Anomaly = "unknown"
		return d
	}
	var svg *html.Node
	var styles []string
	script := false
	walkHTML(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if n.Data == "svg" && svg == nil {
			svg = n
		}
		if n.Data == "script" && (strings.TrimSpace(nodeText(n)) != "" || attr(n, "src") != "") {
			script = true
		}
		if n.Data == "style" {
			styles = append(styles, nodeText(n))
		}
		for _, a := range n.Attr {
			if strings.HasPrefix(strings.ToLower(a.Key), "on") && strings.TrimSpace(a.Val) != "" {
				script = true
			}
			if a.Key == "src" || a.Key == "href" || a.Key == "poster" || a.Key == "srcset" {
				if isExternal(a.Val) {
					d.External = true
				}
			}
			if a.Key == "style" {
				for _, m := range urlRE.FindAllStringSubmatch(a.Val, -1) {
					if isExternal(m[1]) {
						d.External = true
					}
				}
			}
		}
	})
	for _, css := range styles {
		for _, m := range urlRE.FindAllStringSubmatch(css, -1) {
			if isExternal(m[1]) {
				d.External = true
			}
		}
		if strings.Contains(strings.ToLower(css), "@import") {
			d.External = true
		}
	}
	d.Placeholder = placeholderRE.MatchString(raw)
	d.Unclosed = svg != nil && !closedMainSVG(raw)
	valid := svg != nil && !d.Unclosed
	animationPresent := false
	d.ValidSVG = &valid
	if svg == nil {
		d.Elements = pointer(0)
		d.Paths = pointer(0)
		d.Colors = pointer(0)
		d.ViewBox = pointer(false)
		d.Loop = pointer(false)
	} else {
		vb := validViewBox(attr(svg, "viewBox"))
		d.ViewBox = &vb
		rules, keys, unsupported := parseStyles(strings.Join(styles, "\n"))
		elements, paths := 0, 0
		colors := map[string]bool{}
		loop := false
		colorsUnknown := unsupported
		loopUnknown := unsupported
		geometryUnknown := unsupported
		var visit func(*html.Node, map[string]string)
		visit = func(n *html.Node, inherited map[string]string) {
			if n.Type != html.ElementNode {
				return
			}
			if n.Namespace != "svg" {
				return
			}
			name := strings.ToLower(n.Data)
			if definitionTag(name) {
				return
			}
			style := map[string]string{"fill": inherited["fill"], "stroke": inherited["stroke"], "color": inherited["color"]}
			for _, k := range []string{"fill", "stroke", "color", "display", "visibility", "opacity"} {
				if v := attr(n, k); v != "" {
					style[k] = v
				}
			}
			for _, rule := range rules {
				if matchesSelector(n, rule.selector) {
					for k, v := range rule.props {
						style[k] = v
					}
				}
			}
			for k, v := range declarations(attr(n, "style")) {
				style[k] = v
			}
			for _, k := range []string{"d", "r", "rx", "ry", "width", "height", "x", "y", "cx", "cy"} {
				if graphicalTag(name) && (style[k] != "" || unsupportedLengthRE.MatchString(attr(n, k))) {
					geometryUnknown = true
				}
			}
			if name == "tspan" && (attr(n, "fill") != "" || attr(n, "stroke") != "" || attr(n, "style") != "") {
				colorsUnknown = true
			}
			if style["display"] == "none" || style["visibility"] == "hidden" || style["opacity"] == "0" {
				return
			}
			if name == "animate" || name == "animatetransform" || name == "animatemotion" {
				animationPresent = true
				if strings.EqualFold(attr(n, "repeatCount"), "indefinite") && positiveDuration(attr(n, "dur")) {
					loop = true
				}
				if name == "animate" && (attr(n, "attributeName") == "fill" || attr(n, "attributeName") == "stroke") {
					values := attr(n, "values") + ";" + attr(n, "from") + ";" + attr(n, "to")
					for _, v := range strings.Split(values, ";") {
						addColor(colors, v, style["color"], &colorsUnknown)
					}
				}
			}
			if hasAnimation(style) {
				known, used, parsed := cssLoop(style, keys)
				if !parsed {
					loopUnknown = true
				}
				if style["animation"] != "" && (style["animation-name"] != "" || style["animation-duration"] != "" || style["animation-iteration-count"] != "") {
					known = false
					loopUnknown = true
				}
				if len(used) > 0 {
					animationPresent = true
				}
				if known {
					loop = true
				}
				for _, body := range used {
					blocks, ok := cssBlocks(body)
					if !ok {
						loopUnknown = true
						colorsUnknown = true
					}
					for _, b := range blocks {
						for k, v := range declarations(b.body) {
							if k == "fill" || k == "stroke" {
								addColor(colors, v, style["color"], &colorsUnknown)
							}
						}
					}
				}
				if strings.Contains(style["animation"], "var(") || strings.Contains(style["animation-duration"], "var(") || strings.Contains(style["animation-name"], "var(") || strings.Contains(style["animation-iteration-count"], "var(") {
					loopUnknown = true
				}
			}
			if graphicalTag(name) && validGraphic(n) {
				elements++
				if name == "path" {
					paths++
				}
				if name != "image" {
					if name != "line" {
						addColor(colors, style["fill"], style["color"], &colorsUnknown)
					}
					addColor(colors, style["stroke"], style["color"], &colorsUnknown)
				}
				if name == "use" {
					colorsUnknown = true
				} // Referenced paint is not expanded by this static parser.
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				visit(c, style)
			}
		}
		visit(svg, map[string]string{"fill": "black", "stroke": "none", "color": "black"})
		d.Elements = &elements
		d.Paths = &paths
		d.Colors = pointer(len(colors))
		d.Loop = &loop
		if colorsUnknown {
			d.Colors = nil
			d.UnknownMetrics = append(d.UnknownMetrics, "colors")
		}
		if loopUnknown && !loop {
			d.Loop = nil
			d.UnknownMetrics = append(d.UnknownMetrics, "loop")
		}
		if geometryUnknown {
			d.Elements = nil
			d.Paths = nil
			d.UnknownMetrics = append(d.UnknownMetrics, "elements", "paths")
		}
	}
	if script {
		d.ValidSVG = nil
		d.Elements = nil
		d.Paths = nil
		d.Colors = nil
		d.ViewBox = nil
		d.Loop = nil
		d.UnknownMetrics = append(d.UnknownMetrics, "javascript_dynamic_content")
	}
	if d.Unclosed {
		d.ValidSVG = pointer(false)
	}
	if svg == nil && !script {
		d.AnomalyReasons = append(d.AnomalyReasons, "missing_svg")
	}
	if d.Unclosed {
		d.AnomalyReasons = append(d.AnomalyReasons, "unclosed_svg")
	}
	if d.Placeholder {
		d.AnomalyReasons = append(d.AnomalyReasons, "unfinished_placeholder")
	}
	if d.Loop != nil && !animationPresent && !script {
		d.AnomalyReasons = append(d.AnomalyReasons, "no_loop_animation")
	}
	if d.Elements != nil && d.Colors != nil && *d.Elements < 6 && *d.Colors < 2 && d.Bytes < 1500 {
		d.AnomalyReasons = append(d.AnomalyReasons, "minimal_content_all_three")
	}
	d.Anomaly = "not_detected"
	if len(d.UnknownMetrics) > 0 {
		d.Anomaly = "unknown"
	}
	if len(d.AnomalyReasons) > 0 {
		d.Anomaly = "detected"
		d.AnomalyPenalty = 30
	}
	// The breakdown is the only place points are computed; the totals below are
	// derived from it, so the published rows and the stored score cannot drift.
	d.Metrics = []DrawingMetric{
		{Key: "valid_svg", Value: boolValue(d.ValidSVG), Points: boolPoints(d.ValidSVG, 40), Max: 40, Known: d.ValidSVG != nil},
		{Key: "loop", Value: boolValue(d.Loop), Points: boolPoints(d.Loop, 25), Max: 25, Known: d.Loop != nil},
		{Key: "elements", Value: intValue(d.Elements), Points: intPoints(d.Elements, func(n int) int { return tierPoints(n, 6, 15, 5, 10) }), Max: 10, Known: d.Elements != nil},
		{Key: "paths", Value: intValue(d.Paths), Points: intPoints(d.Paths, func(n int) int { return tierPoints(n, 5, 5, 5, 5) }), Max: 5, Known: d.Paths != nil},
		{Key: "colors", Value: intValue(d.Colors), Points: intPoints(d.Colors, func(n int) int { return tierPoints(n, 2, 4, 5, 10) }), Max: 10, Known: d.Colors != nil},
		{Key: "bytes", Value: strconv.Itoa(d.Bytes), Points: tierPoints(d.Bytes, 1500, 4096, 5, 10), Max: 10, Known: true},
		{Key: "view_box", Value: boolValue(d.ViewBox), Points: boolPoints(d.ViewBox, 5), Max: 5, Known: d.ViewBox != nil},
	}
	if d.External {
		d.Metrics = append(d.Metrics, DrawingMetric{Key: "external", Points: 25, Max: 25, Penalty: true, Known: true})
	}
	if d.Placeholder {
		d.Metrics = append(d.Metrics, DrawingMetric{Key: "placeholder", Points: 20, Max: 20, Penalty: true, Known: true})
	}
	if d.Unclosed {
		d.Metrics = append(d.Metrics, DrawingMetric{Key: "unclosed", Points: 20, Max: 20, Penalty: true, Known: true})
	}
	if d.AnomalyPenalty > 0 {
		d.Metrics = append(d.Metrics, DrawingMetric{Key: "anomaly", Value: strings.Join(d.AnomalyReasons, ", "), Points: d.AnomalyPenalty, Max: 30, Penalty: true, Known: true})
	}
	d.StructuralPoints, d.StructuralPenalty, d.AnomalyPenalty = 0, 0, 0
	for _, metric := range d.Metrics {
		if !metric.Penalty {
			d.StructuralPoints += metric.Points
			continue
		}
		if metric.Key == "anomaly" {
			d.AnomalyPenalty += metric.Points
			continue
		}
		d.StructuralPenalty += metric.Points
	}
	return d
}

func boolValue(v *bool) string {
	if v == nil {
		return ""
	}
	return map[bool]string{true: "yes", false: "no"}[*v]
}

func boolPoints(v *bool, points int) int {
	if v != nil && *v {
		return points
	}
	return 0
}

func intValue(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func intPoints(v *int, points func(int) int) int {
	if v == nil {
		return 0
	}
	return points(*v)
}

func tierPoints(n, low, high, lowPoints, highPoints int) int {
	if n >= high {
		return highPoints
	}
	if n >= low {
		return lowPoints
	}
	return 0
}
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	walkHTML(n, func(c *html.Node) {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	})
	return b.String()
}
func walkHTML(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkHTML(c, fn)
	}
}
func isExternal(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && !strings.HasPrefix(v, "#") && !strings.HasPrefix(strings.ToLower(v), "data:")
}
func definitionTag(n string) bool {
	switch n {
	case "defs", "clippath", "mask", "pattern", "symbol", "marker":
		return true
	}
	return false
}
func graphicalTag(n string) bool {
	switch n {
	case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text", "image", "use":
		return true
	}
	return false
}
func finiteNumber(s string) (float64, bool) {
	v, e := strconv.ParseFloat(s, 64)
	return v, e == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
}
func positive(s string) bool { v, ok := finiteNumber(s); return ok && v > 0 }
func validGraphic(n *html.Node) bool {
	switch strings.ToLower(n.Data) {
	case "path":
		return validPath(attr(n, "d"))
	case "circle":
		return positiveLength(attr(n, "r"))
	case "ellipse":
		return positiveLength(attr(n, "rx")) && positiveLength(attr(n, "ry"))
	case "rect", "image":
		return positiveLength(attr(n, "width")) && positiveLength(attr(n, "height"))
	case "polyline", "polygon":
		points := strings.Fields(strings.ReplaceAll(attr(n, "points"), ",", " "))
		minimum := 4
		if n.Data == "polygon" {
			minimum = 6
		}
		if len(points) < minimum || len(points)%2 != 0 {
			return false
		}
		for _, p := range points {
			if _, ok := finiteNumber(p); !ok {
				return false
			}
		}
		return true
	case "text":
		return strings.TrimSpace(nodeText(n)) != ""
	case "use":
		return attr(n, "href") != ""
	case "line":
		var coords [4]float64
		for i, k := range []string{"x1", "y1", "x2", "y2"} {
			if s := attr(n, k); s != "" {
				v, ok := finiteNumber(s)
				if !ok {
					return false
				}
				coords[i] = v
			}
		}
		return coords[0] != coords[2] || coords[1] != coords[3]
	}
	return false
}
func positiveLength(s string) bool {
	return positive(strings.TrimSuffix(strings.TrimSuffix(s, "px"), "%"))
}

var unsupportedLengthRE = regexp.MustCompile(`(?i)(?:em|rem|vw|vh|vmin|vmax|cm|mm|in|pt|pc)$|^(?:calc|var)\(`)

var pathTokenRE = regexp.MustCompile(`[AaCcHhLlMmQqSsTtVvZz]|[-+]?(?:\d*\.\d+|\d+\.?\d*)(?:[eE][-+]?\d+)?`)

func validPath(d string) bool {
	// Validate command arity without interpreting or rendering generated paths.
	tokens := pathTokenRE.FindAllStringIndex(d, -1)
	if len(tokens) == 0 {
		return false
	}
	last := 0
	command := byte(0)
	count := 0
	arity := 0
	for _, p := range tokens {
		if strings.Trim(d[last:p[0]], " ,\t\r\n") != "" {
			return false
		}
		token := d[p[0]:p[1]]
		last = p[1]
		if len(token) == 1 && strings.ContainsAny(token, "AaCcHhLlMmQqSsTtVvZz") {
			if command != 0 && (count < arity || (arity > 0 && count%arity != 0)) {
				return false
			}
			command = token[0]
			if p[0] == tokens[0][0] && command != 'M' && command != 'm' {
				return false
			}
			count = 0
			switch command {
			case 'Z', 'z':
				arity = 0
			case 'H', 'h', 'V', 'v':
				arity = 1
			case 'M', 'm', 'L', 'l', 'T', 't':
				arity = 2
			case 'S', 's', 'Q', 'q':
				arity = 4
			case 'C', 'c':
				arity = 6
			case 'A', 'a':
				arity = 7
			}
		} else {
			if command == 0 || arity == 0 {
				return false
			}
			if _, ok := finiteNumber(token); !ok {
				return false
			}
			count++
		}
	}
	return strings.Trim(d[last:], " ,\t\r\n") == "" && count >= arity && (arity == 0 || count%arity == 0)
}
func validViewBox(s string) bool {
	v := strings.Fields(strings.ReplaceAll(s, ",", " "))
	if len(v) != 4 {
		return false
	}
	for _, x := range v {
		if _, ok := finiteNumber(x); !ok {
			return false
		}
	}
	return positive(v[2]) && positive(v[3])
}
func positiveDuration(s string) bool {
	m := durationRE.FindStringSubmatch(strings.TrimSpace(s))
	return len(m) > 0 && positive(m[1])
}

// HTML's parser repairs incomplete foreign content. Check original tokens instead
// so the repaired tree cannot award closure points to an unfinished response.
func closedMainSVG(raw string) bool {
	z := html.NewTokenizer(strings.NewReader(raw))
	var stack []string
	started := false
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return z.Err() == io.EOF && started && len(stack) == 0
		}
		t := z.Token()
		if !started {
			if (tt == html.StartTagToken || tt == html.SelfClosingTagToken) && t.Data == "svg" {
				started = true
				if tt == html.SelfClosingTagToken {
					return true
				}
				stack = append(stack, t.Data)
			}
			continue
		}
		if tt == html.StartTagToken {
			stack = append(stack, t.Data)
		}
		if tt == html.EndTagToken {
			if len(stack) == 0 || stack[len(stack)-1] != t.Data {
				return false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return true
			}
		}
	}
}

func addColor(colors map[string]bool, v, current string, unknown *bool) {
	if strings.EqualFold(v, "currentcolor") {
		v = current
	}
	key, ok := normalizeColor(v)
	if !ok {
		*unknown = true
		return
	}
	if key != "" {
		colors[key] = true
	}
}

func normalizeColor(value string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" || v == "none" || v == "transparent" {
		return "", true
	}
	if c, ok := colornames.Map[v]; ok {
		return fmt.Sprintf("%02x%02x%02x%02x", c.R, c.G, c.B, c.A), true
	}
	if strings.HasPrefix(v, "#") {
		h := v[1:]
		if len(h) == 3 || len(h) == 4 {
			var b strings.Builder
			for _, r := range h {
				b.WriteRune(r)
				b.WriteRune(r)
			}
			h = b.String()
		}
		if len(h) == 6 {
			h += "ff"
		}
		if len(h) != 8 {
			return "", false
		}
		if _, e := strconv.ParseUint(h, 16, 32); e != nil {
			return "", false
		}
		if h[6:] == "00" {
			return "", true
		}
		return h, true
	}
	open := strings.IndexByte(v, '(')
	if open < 0 || !strings.HasSuffix(v, ")") {
		return "", false
	}
	name := v[:open]
	args := strings.Fields(strings.NewReplacer(",", " ", "/", " ").Replace(v[open+1 : len(v)-1]))
	if len(args) < 3 || len(args) > 4 {
		return "", false
	}
	a := 1.0
	if len(args) == 4 {
		var ok bool
		a, ok = colorComponent(args[3], 1)
		if !ok {
			return "", false
		}
	}
	if a == 0 {
		return "", true
	}
	var r, g, b float64
	if name == "rgb" || name == "rgba" {
		var ok bool
		r, ok = colorComponent(args[0], 255)
		if !ok {
			return "", false
		}
		g, ok = colorComponent(args[1], 255)
		if !ok {
			return "", false
		}
		b, ok = colorComponent(args[2], 255)
		if !ok {
			return "", false
		}
	} else if name == "hsl" || name == "hsla" {
		h, ok := finiteNumber(strings.TrimSuffix(args[0], "deg"))
		if !ok {
			return "", false
		}
		s, ok := colorComponent(args[1], 1)
		if !ok || !strings.HasSuffix(args[1], "%") {
			return "", false
		}
		l, ok := colorComponent(args[2], 1)
		if !ok || !strings.HasSuffix(args[2], "%") {
			return "", false
		}
		h = math.Mod(math.Mod(h, 360)+360, 360) / 60
		c := (1 - math.Abs(2*l-1)) * s
		x := c * (1 - math.Abs(math.Mod(h, 2)-1))
		m := l - c/2
		switch {
		case h < 1:
			r, g, b = c, x, 0
		case h < 2:
			r, g, b = x, c, 0
		case h < 3:
			r, g, b = 0, c, x
		case h < 4:
			r, g, b = 0, x, c
		case h < 5:
			r, g, b = x, 0, c
		default:
			r, g, b = c, 0, x
		}
		r = (r + m) * 255
		g = (g + m) * 255
		b = (b + m) * 255
	} else {
		return "", false
	}
	return fmt.Sprintf("%02x%02x%02x%02x", int(math.Round(r)), int(math.Round(g)), int(math.Round(b)), int(math.Round(a*255))), true
}
func colorComponent(s string, scale float64) (float64, bool) {
	percent := strings.HasSuffix(s, "%")
	v, ok := finiteNumber(strings.TrimSuffix(s, "%"))
	if !ok {
		return 0, false
	}
	if percent {
		v = v * scale / 100
	}
	return max(0, min(scale, v)), true
}

type cssBlock struct{ head, body string }
type cssRule struct {
	selector string
	props    map[string]string
}

func cssBlocks(css string) ([]cssBlock, bool) {
	css = cssCommentRE.ReplaceAllString(css, "")
	var out []cssBlock
	start := 0
	depth := 0
	bodyStart := 0
	head := ""
	quote := byte(0)
	for i := 0; i < len(css); i++ {
		c := css[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '{' {
			if depth == 0 {
				head = strings.TrimSpace(css[start:i])
				bodyStart = i + 1
			}
			depth++
		}
		if c == '}' {
			depth--
			if depth < 0 {
				return nil, false
			}
			if depth == 0 {
				out = append(out, cssBlock{head, css[bodyStart:i]})
				start = i + 1
			}
		}
	}
	return out, depth == 0 && quote == 0 && strings.TrimSpace(css[start:]) == ""
}
func declarations(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		kv := strings.SplitN(part, ":", 2)
		if len(kv) == 2 {
			out[strings.ToLower(strings.TrimSpace(kv[0]))] = strings.TrimSpace(kv[1])
		}
	}
	return out
}

var simpleSelectorRE = regexp.MustCompile(`^(?:[A-Za-z][\w-]*|\*)?(?:[.#][\w-]+)*$`)
var selectorTokenRE = regexp.MustCompile(`^[A-Za-z][\w-]*|[.#][\w-]+`)

func parseStyles(s string) ([]cssRule, map[string]string, bool) {
	var rules []cssRule
	keys := map[string]string{}
	blocks, parsed := cssBlocks(s)
	unsupported := !parsed
	for _, b := range blocks {
		if strings.HasPrefix(b.head, "@keyframes ") || strings.HasPrefix(b.head, "@-webkit-keyframes ") {
			f, ok := splitCSSValue(b.head, false)
			if ok && len(f) == 2 && !strings.Contains(f[1], `\`) {
				keys[strings.Trim(f[1], `"'`)] = b.body
			} else {
				unsupported = true
			}
			continue
		}
		if strings.HasPrefix(b.head, "@") {
			unsupported = true
			continue
		}
		for _, selector := range strings.Split(b.head, ",") {
			selector = strings.TrimSpace(selector)
			if !simpleSelectorRE.MatchString(selector) {
				unsupported = true
				continue
			}
			rules = append(rules, cssRule{selector, declarations(b.body)})
		}
	}
	sort.SliceStable(rules, func(i, j int) bool { return specificity(rules[i].selector) < specificity(rules[j].selector) })
	return rules, keys, unsupported
}
func specificity(s string) int {
	n := strings.Count(s, "#")*100 + strings.Count(s, ".")*10
	if s != "" && s[0] != '.' && s[0] != '#' && s[0] != '*' {
		n++
	}
	return n
}
func matchesSelector(n *html.Node, s string) bool {
	if s == "*" {
		return true
	}
	for _, p := range selectorTokenRE.FindAllString(s, -1) {
		switch p[0] {
		case '.':
			found := false
			for _, c := range strings.Fields(attr(n, "class")) {
				if c == p[1:] {
					found = true
				}
			}
			if !found {
				return false
			}
		case '#':
			if attr(n, "id") != p[1:] {
				return false
			}
		default:
			if !strings.EqualFold(n.Data, p) {
				return false
			}
		}
	}
	return true
}
func hasAnimation(m map[string]string) bool { return m["animation"] != "" || m["animation-name"] != "" }

// Split only at the top level: timing functions and quoted names can contain commas or spaces.
func splitCSSValue(s string, comma bool) ([]string, bool) {
	var out []string
	var brackets []byte
	start, quote := 0, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			// ponytail: CSS escapes stay unknown; add identifier decoding if needed.
			return nil, false
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(', '[':
			brackets = append(brackets, c)
		case ')', ']':
			if len(brackets) == 0 || (c == ')' && brackets[len(brackets)-1] != '(') || (c == ']' && brackets[len(brackets)-1] != '[') {
				return nil, false
			}
			brackets = brackets[:len(brackets)-1]
		}
		separator := c == ','
		if !comma {
			separator = c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
		}
		if separator && quote == 0 && len(brackets) == 0 {
			part := strings.TrimSpace(s[start:i])
			if part != "" {
				out = append(out, part)
			} else if comma {
				return nil, false
			}
			start = i + 1
		}
	}
	if quote != 0 || len(brackets) != 0 {
		return nil, false
	}
	part := strings.TrimSpace(s[start:])
	if part != "" {
		out = append(out, part)
	} else if comma && len(out) > 0 {
		return nil, false
	}
	return out, true
}

func cssLoop(style map[string]string, keys map[string]string) (bool, []string, bool) {
	var used []string
	loop := false
	if shorthand := style["animation"]; shorthand != "" {
		items, ok := splitCSSValue(shorthand, true)
		if !ok {
			return false, nil, false
		}
		for _, item := range items {
			fields, ok := splitCSSValue(item, false)
			if !ok {
				return false, nil, false
			}
			name := ""
			duration := false
			timeSeen := false
			infinite := false
			for _, v := range fields {
				candidate := strings.Trim(v, `"'`)
				if _, ok := keys[candidate]; ok {
					name = candidate
				}
				if strings.HasSuffix(v, "s") && durationRE.MatchString(v) && !timeSeen {
					duration = positiveDuration(v)
					timeSeen = true
				}
				if v == "infinite" {
					infinite = true
				}
			}
			if name != "" {
				used = append(used, keys[name])
				if duration && infinite {
					loop = true
				}
			}
		}
	}
	names, namesOK := splitCSSValue(style["animation-name"], true)
	durations, durationsOK := splitCSSValue(style["animation-duration"], true)
	counts, countsOK := splitCSSValue(style["animation-iteration-count"], true)
	if !namesOK || !durationsOK || !countsOK {
		return false, nil, false
	}
	for i, name := range names {
		name = strings.Trim(name, `"'`)
		if body, ok := keys[name]; ok {
			used = append(used, body)
			if len(durations) == 0 || len(counts) == 0 {
				continue
			}
			duration := strings.TrimSpace(durations[i%len(durations)])
			if strings.HasSuffix(duration, "s") && positiveDuration(duration) && strings.TrimSpace(counts[i%len(counts)]) == "infinite" {
				loop = true
			}
		}
	}
	return loop, used, true
}
