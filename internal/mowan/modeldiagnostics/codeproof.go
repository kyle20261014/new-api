package modeldiagnostics

import (
	"regexp"
	"strings"
)

// This is a whole-program whitelist, not a JavaScript interpreter. The loop's
// invariant is sum=2+4+...+(i-2); the guarded formula is 2*(1+...+k)=k*(k+1).
// Proof scope: finite integer inputs whose result is a safe JS integer, and
// standard Math.floor. Unsupported programs are never executed or called wrong.
func proveEvenSum(reply string) (*bool, string) {
	if len(reply) > 16*1024 {
		return nil, "code_not_in_proven_subset"
	}
	code := strings.TrimSpace(reply)
	if strings.HasPrefix(code, "```") {
		line := strings.IndexByte(code, '\n')
		if line < 0 || len(code)-4 < line+1 || !strings.HasSuffix(code, "\n```") {
			return nil, "code_not_in_proven_subset"
		}
		header := strings.TrimSpace(code[:line])
		if header != "```" && header != "```js" && header != "```javascript" {
			return nil, "code_not_in_proven_subset"
		}
		code = code[line+1 : len(code)-4]
	}
	tokens := proofTokens(code)
	for _, pattern := range []struct{ code, reason string }{
		{`function sumEven(n) { MUTABLE $sum = 0; for (MUTABLE $i = 2; $i <= n; $i += 2) { $sum += $i; } return $sum; }`, "static_even_sum_loop_proven_for_safe_integer_sums"},
		{`function sumEven(n) { MUTABLE $sum = 0; for (MUTABLE $i = 2; $i <= n; $i += 2) $sum += $i; return $sum; }`, "static_even_sum_loop_proven_for_safe_integer_sums"},
		{`function sumEven(n) { if (n < 2) return 0; LOCAL $k = Math.floor(n / 2); return $k * ($k + 1); }`, "static_even_sum_formula_proven_for_safe_integer_sums"},
		{`function sumEven(n) { if (n < 2) { return 0; } LOCAL $k = Math.floor(n / 2); return $k * ($k + 1); }`, "static_even_sum_formula_proven_for_safe_integer_sums"},
	} {
		if matchProof(tokens, proofTokens(pattern.code)) {
			return pointer(true), pattern.reason
		}
	}
	return nil, "code_not_in_proven_subset"
}

var proofTokenRE = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*|[012]|<=|\+=|[(){};=+*/.,<]`)
var proofIdentifierRE = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func proofTokens(code string) []string {
	var tokens []string
	last := 0
	for _, p := range proofTokenRE.FindAllStringIndex(code, -1) {
		gap := code[last:p[0]]
		if strings.Trim(gap, " \t\r\n") != "" || (len(tokens) > 0 && tokens[len(tokens)-1] == "return" && strings.ContainsAny(gap, "\r\n")) {
			return nil
		}
		tokens = append(tokens, code[p[0]:p[1]])
		last = p[1]
	}
	if strings.Trim(code[last:], " \t\r\n") != "" {
		return nil
	}
	return tokens
}

func matchProof(tokens, pattern []string) bool {
	if len(tokens) == 0 || len(tokens) != len(pattern) {
		return false
	}
	bindings := map[string]string{}
	for i, want := range pattern {
		got := tokens[i]
		switch want {
		case "MUTABLE", "LOCAL":
			if got != "let" && got != "var" && !(want == "LOCAL" && got == "const") {
				return false
			}
		case "$sum", "$i", "$k":
			if bound, ok := bindings[want]; ok {
				if bound != got {
					return false
				}
				continue
			}
			if !proofIdentifierRE.MatchString(got) || strings.Contains(" n sumEven Math arguments eval await yield break case catch class const continue debugger default delete do else enum export extends false finally for function if implements import in instanceof interface let new null package private protected public return static super switch this throw true try typeof var void while with ", " "+got+" ") {
				return false
			}
			for _, bound := range bindings {
				if bound == got {
					return false
				}
			}
			bindings[want] = got
		default:
			if got != want {
				return false
			}
		}
	}
	return true
}
