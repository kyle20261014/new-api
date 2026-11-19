// Package modeldiagnostics builds paid probe plans and evaluates saved replies locally.
// It does not send requests, execute generated code, or perform billing.
package modeldiagnostics

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

const Version = "mowan-diagnostics-v1"

// CurrentVersion is metadata only: it never generates challenges or materials.
func CurrentVersion(kind string) string {
	switch kind {
	case "connection":
		return Version
	case "fingerprint":
		return FingerprintVersion
	case "intelligence":
		return IntelligenceVersion
	case "drawing":
		return DrawingVersion
	default:
		return ""
	}
}

type Probe struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Prompt        string `json:"prompt"`
	Weight        int    `json:"weight"`
	MaxTokens     int    `json:"max_tokens"`
	ExpectedCount int    `json:"expected_count,omitempty"`
}
type Plan struct {
	Version   string            `json:"version"`
	Steps     []Probe           `json:"steps"`
	Materials map[string]string `json:"materials,omitempty"`
}
type Answer struct {
	ID            string `json:"id"`
	Text          string `json:"text"`
	Complete      bool   `json:"complete"`
	Source        string `json:"-"`
	ExpectedCount int    `json:"expected_count,omitempty"`
}
type Result struct {
	Status   string   `json:"status"`
	Score    *int     `json:"score"`
	Passed   *bool    `json:"passed"`
	Complete bool     `json:"complete"`
	Reasons  []string `json:"reasons"`
	Details  any      `json:"details"`
	Version  string   `json:"version"`
}

const DrawingPrompt = "请生成可直接运行的单文件HTML，使用内联SVG绘制鹈鹕骑自行车的二维循环动画。画面以鹈鹕和自行车为主体，展示清晰的身体结构、踩踏动作和车轮转动，配合协调的背景、配色与层次。动画应流畅自然、衔接连续，并适配不同屏幕尺寸。禁止依赖外部资源，只输出完整HTML，不要代码围栏或解释文字。"

// These materials are this platform's adapted pool, not the reference site's private library.
var materialPool = []struct {
	key    string
	values []string
}{
	{"theme", []string{"测量与直觉的交锋", "秩序与偶然的相遇", "时间与记忆的追逐"}},
	{"subject", []string{"杏鲍菇", "鹈鹕", "小机器人"}},
	{"element_one", []string{"金属顶针", "纸风车", "铜铃"}},
	{"element_two", []string{"水平仪", "指南针", "沙漏"}},
	{"scene", []string{"彩虹检票站候车区", "雨后的屋顶花园", "月光下的钟表工坊"}},
	{"action", []string{"沿八字轨迹连续穿行", "绕圆形轨道交替追逐", "上下起伏并回到起点"}},
	{"style", []string{"漆画金线与纯色底面", "剪纸与平面几何", "清晰线描与柔和色块"}},
	{"palette", []string{"纯白与番茄红配强亮浅橙逆光", "深蓝与暖黄", "青绿与珊瑚橙"}},
}

func NewPlan(kind string, creative bool) (Plan, error) {
	p := Plan{Version: Version}
	if creative && kind != "drawing" {
		return p, fmt.Errorf("creative mode requires drawing kind")
	}
	switch kind {
	case "connection":
		p.Steps = []Probe{{ID: "connection", Title: "通联", Prompt: "请简短回复：你好。", MaxTokens: 64}}
	case "fingerprint":
		return fingerprintPlan()
	case "intelligence":
		p.Version = IntelligenceVersion
		for _, q := range intelligenceQuestions {
			p.Steps = append(p.Steps, q.Probe)
		}
	case "drawing":
		p.Version = DrawingVersion
		prompt := DrawingPrompt
		if creative {
			p.Materials = map[string]string{"pool": "mowan-adapted-v1"}
			for _, field := range materialPool {
				n, err := rand.Int(rand.Reader, big.NewInt(int64(len(field.values))))
				if err != nil {
					return Plan{}, fmt.Errorf("select drawing material %s: %w", field.key, err)
				}
				p.Materials[field.key] = field.values[n.Int64()]
			}
			prompt = fmt.Sprintf("请生成可直接运行的单文件HTML＋内联SVG二维循环动画。主题：%s；主体：%s；元素一：%s；元素二：%s；场景：%s；动作：%s；风格：%s；配色：%s。主题、主体、两项元素、场景、动作、风格和配色都应在作品中可辨认；两项元素参与动作，场景影响事件，动作衔接成循环。不依赖外部资源，只输出完整HTML，不输出代码围栏或解释。", p.Materials["theme"], p.Materials["subject"], p.Materials["element_one"], p.Materials["element_two"], p.Materials["scene"], p.Materials["action"], p.Materials["style"], p.Materials["palette"])
		}
		p.Steps = []Probe{{ID: "drawing", Title: "HTML/SVG 动画", Prompt: prompt, MaxTokens: 12000}}
	default:
		return p, fmt.Errorf("unsupported diagnostic kind %q", kind)
	}
	return p, nil
}

func Evaluate(kind string, answers []Answer) Result {
	// Duplicate IDs are ambiguous and must never produce a completed score.
	seen := map[string]bool{}
	for _, a := range answers {
		if seen[a.ID] {
			return Result{Status: "unknown", Reasons: []string{"duplicate_answer_id"}, Version: Version}
		}
		seen[a.ID] = true
	}
	switch kind {
	case "connection":
		ok := len(answers) == 1 && answers[0].ID == "connection" && answers[0].Complete && strings.TrimSpace(answers[0].Text) != ""
		if !ok {
			return Result{Status: "incomplete", Reasons: []string{"no_complete_reply"}, Version: Version}
		}
		return Result{Status: "completed", Passed: &ok, Complete: true, Reasons: []string{}, Details: answers, Version: Version}
	case "fingerprint":
		return evaluateFingerprint(answers)
	case "intelligence":
		return evaluateIntelligence(answers)
	case "drawing":
		return evaluateDrawing(answers)
	default:
		return Result{Status: "unknown", Reasons: []string{"unsupported_kind"}, Version: Version}
	}
}

func pointer[T any](v T) *T { return &v }
