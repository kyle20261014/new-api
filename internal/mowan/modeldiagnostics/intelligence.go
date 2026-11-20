package modeldiagnostics

// Question data and answer normalization adapted from Vhow123/dsh-model-probe
// 8f97b83b83fd708ee2985e8bbcfeedf56c078d64, MIT; see LICENSE.dsh-model-probe.
import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
)

// v3 adds a guarded closed-form proof for sumEven; reports graded by v2 stay
// stored under v2 and are not re-scored.
const IntelligenceVersion = "dsh-model-probe-8f97b83-mowan-grader-v3"

type question struct {
	Probe
	Expected string
}

var intelligenceQuestions = []question{
	{Probe{"candy-canary", "logic", "小明有 3 颗糖，吃掉 1 颗后又放入 2 颗。现在有几颗糖？只输出数字。", 1, 256, 0}, "4"},
	{Probe{"logic-chain", "logic", "所有甲都是乙，所有乙都是丙。已知甲存在，能否确定甲是丙？只输出“是”或“否”。", 2, 384, 0}, "是"},
	{Probe{"logic-order", "logic", "甲、乙、丙、丁排队：甲在乙前面，乙在丙前面，丁在丙后面。谁排第二？只输出名字。", 2, 384, 0}, "乙"},
	{Probe{"logic-converse", "logic", "只有今天下雨，小明才带伞。已知小明今天带伞，能否必然推出今天下雨？只输出“是”或“否”。", 2, 384, 0}, "是"},
	{Probe{"math-arithmetic", "math", "计算 48 ÷ 6 + 7 × 3。只输出数字。", 1, 256, 0}, "29"},
	{Probe{"math-rectangle", "math", "长方形周长是 30，长是 9。它的面积是多少？只输出数字。", 2, 384, 0}, "54"},
	{Probe{"math-sequence", "math", "数列 2，6，12，20，30 的下一项是什么？只输出数字。", 2, 384, 0}, "42"},
	{Probe{"math-hard-roots", "math-hard", "整数 x、y 满足 x + y = 17 且 xy = 60。求 x² + y²。只输出数字。", 3, 512, 0}, "169"},
	{Probe{"math-hard-probability", "math-hard", "袋中有 5 个红球和 3 个蓝球，不放回地取 2 个球。两个都是红球的概率是多少？只输出最简分数，例如 1/2。", 3, 512, 0}, "5/14"},
	{Probe{"math-hard-recurrence", "math-hard", "数列满足 a₀ = 0，且 aₙ = 2aₙ₋₁ + 1（n ≥ 1）。求 a₆。只输出数字。", 3, 512, 0}, "63"},
	{Probe{"code-function", "code", "写一个 JavaScript 函数 sumEven(n)，返回 2 到 n（含 n）之间所有偶数的和。只输出代码，不要解释。", 2, 768, 0}, "sumEven"},
	{Probe{"code-output", "code", "以下 JavaScript 的输出是什么？console.log([1,2,3].map(x => x * 2).filter(x => x > 2).join(\"-\"))。只输出结果。", 2, 384, 0}, "4-6"},
	{Probe{"instruction-token", "instruction", "只输出 DSH-IQ-731，不能有任何其他字符、标点或 Markdown。", 1, 128, 0}, "DSH-IQ-731"},
	{Probe{"instruction-json", "instruction", "只输出这个 JSON 对象，不要 Markdown 或解释：{\"ok\":true,\"count\":3,\"tag\":\"alpha\"}", 2, 256, 0}, "{\"ok\":true,\"count\":3,\"tag\":\"alpha\"}"},
	{Probe{"instruction-lines", "instruction", "只输出三行，顺序必须是 RED、GREEN、BLUE，每行一个词，不要其他文字。", 2, 256, 0}, "RED\nGREEN\nBLUE"},
	{Probe{"knowledge-water", "knowledge", "水的化学式是什么？只输出化学式。", 1, 128, 0}, "H2O"},
	{Probe{"trap-rooster", "trap", "一只公鸡在屋顶上下了一个蛋，蛋会往哪边滚？只输出一句简短回答。", 2, 384, 0}, "公鸡不会下蛋"},
	{Probe{"trap-weight", "trap", "1 千克铁和 1 千克棉花哪个更重？只输出“一样重”或“不一样重”。", 2, 384, 0}, "一样重"},
}

type IntelligenceItem struct {
	ID       string `json:"id"`
	Prompt   string `json:"prompt"`
	Expected string `json:"expected"`
	Reply    string `json:"reply"`
	Weight   int    `json:"weight"`
	Complete bool   `json:"complete"`
	Passed   *bool  `json:"passed"`
	Reason   string `json:"reason"`
}
type IntelligenceDetails struct {
	Items           []IntelligenceItem `json:"items"`
	Completed       int                `json:"completed"`
	Judged          int                `json:"judged"`
	Total           int                `json:"total"`
	WeightedGot     int                `json:"weighted_got"`
	WeightedMax     int                `json:"weighted_max"`
	QuestionVersion string             `json:"question_version"`
}

func evaluateIntelligence(answers []Answer) Result {
	d := IntelligenceDetails{Total: 18, QuestionVersion: "8f97b83b83fd708ee2985e8bbcfeedf56c078d64"}
	byID := map[string]Answer{}
	for _, a := range answers {
		byID[a.ID] = a
	}
	for _, q := range intelligenceQuestions {
		d.WeightedMax += q.Weight
		a := byID[q.ID]
		item := IntelligenceItem{ID: q.ID, Prompt: q.Prompt, Expected: q.Expected, Reply: a.Text, Weight: q.Weight, Complete: a.Complete, Reason: "not_completed"}
		if a.Complete {
			d.Completed++
			item.Passed, item.Reason = gradeIntelligence(q, a.Text)
			if item.Passed != nil {
				d.Judged++
				if *item.Passed {
					d.WeightedGot += q.Weight
				}
			}
		}
		d.Items = append(d.Items, item)
	}
	r := Result{Status: "incomplete", Reasons: []string{}, Details: d, Version: IntelligenceVersion}
	if d.Completed != d.Total {
		r.Reasons = []string{"not_all_questions_completed"}
		return r
	}
	if d.Judged != d.Total {
		r.Status = "unknown"
		r.Reasons = []string{"code_not_in_proven_subset"}
		return r
	}
	r.Complete = true
	r.Status = "completed"
	r.Score = pointer(int(math.Round(float64(d.WeightedGot) * 100 / float64(d.WeightedMax))))
	r.Passed = pointer(*r.Score >= 70)
	return r
}

var fenceStart = regexp.MustCompile("(?i)^```(?:json|javascript|js|text|math)?\\s*")
var markedAnswer = regexp.MustCompile(`(?im)(?:^|\n)\s*(?:ANSWER|答案)\s*[:：]\s*([^\n]+)\s*$`)
var numberAnswer = regexp.MustCompile(`^-?\d+(?:\.\d+)?$`)
var roosterAnswer = regexp.MustCompile(`公鸡.*(?:不会|不能|不下蛋)|(?:公鸡|雄鸡).*(?:没有|无).*蛋`)

func finalAnswer(s string) string {
	s = strings.TrimSpace(s)
	s = fenceStart.ReplaceAllString(s, "")
	s = strings.TrimSpace(strings.TrimSuffix(s, "```"))
	if m := markedAnswer.FindStringSubmatch(s); len(m) > 0 {
		s = m[1]
	}
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), "。．"))
}
func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '０' && r <= '９' {
			return r - 65248
		}
		if r == '／' {
			return '/'
		}
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
func gradeIntelligence(q question, reply string) (*bool, string) {
	if q.ID == "code-function" {
		return proveEvenSum(reply)
	}
	a := finalAnswer(reply)
	c := digits(a)
	ok := false
	switch q.ID {
	case "candy-canary", "math-arithmetic", "math-rectangle", "math-sequence", "math-hard-roots", "math-hard-recurrence":
		c = strings.TrimPrefix(strings.TrimSuffix(c, ")"), "(")
		if numberAnswer.MatchString(c) {
			got, e := strconv.ParseFloat(c, 64)
			want, _ := strconv.ParseFloat(q.Expected, 64)
			ok = e == nil && got == want
		}
	case "math-hard-probability":
		ok = c == "5/14" || c == `\frac{5}{14}`
	case "logic-chain", "logic-converse":
		ok = a == "是" || strings.EqualFold(a, "yes")
	case "logic-order":
		ok = a == "乙"
	case "code-output":
		ok = c == "4-6"
	case "instruction-token":
		ok = a == q.Expected
	case "instruction-json":
		var obj map[string]any
		if common.Unmarshal([]byte(a), &obj) == nil {
			ok = len(obj) == 3 && obj["ok"] == true && obj["count"] == float64(3) && obj["tag"] == "alpha"
		}
	case "instruction-lines":
		lines := strings.Split(strings.ReplaceAll(a, "\r", ""), "\n")
		for i := range lines {
			lines[i] = strings.TrimSpace(lines[i])
		}
		ok = strings.Join(lines, "\n") == q.Expected
	case "knowledge-water":
		ok = strings.EqualFold(c, "H2O") || strings.EqualFold(c, "H₂O")
	case "trap-rooster":
		ok = roosterAnswer.MatchString(a)
	case "trap-weight":
		ok = a == "一样重" || a == "同样重" || a == "相等" || a == "一样"
	}
	if ok {
		return &ok, "matches_expected_answer"
	}
	return &ok, "answer_mismatch"
}
