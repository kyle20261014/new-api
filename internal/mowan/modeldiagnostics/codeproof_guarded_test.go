package modeldiagnostics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The reported answer from the 2026-11-20 production run: a guarded closed
// form, not the loop or the two-statement formula the whitelist already knew.
const reportedGuardedCode = "function sumEven(n) {\n  const k = Math.floor(n / 2);\n  return k > 0 ? k * (k + 1) : 0;\n}"

func TestProveEvenSumAcceptsReportedGuardedFormula(t *testing.T) {
	for _, reply := range []string{
		reportedGuardedCode,
		"```js\n" + reportedGuardedCode + "\n```",
		"function sumEven(n) { const k = Math.floor(n / 2); return k > 0 ? (k * (k + 1)) : 0; }",
		"\n" + reportedGuardedCode + "\n",
	} {
		passed, reason := proveEvenSum(reply)
		require.NotNil(t, passed, reason)
		require.True(t, *passed)
		require.Contains(t, reason, "static_even_sum_guarded_formula_proven")
	}
}

func TestProveEvenSumRejectsNearMisses(t *testing.T) {
	for _, reply := range []string{
		// Wrong arithmetic: k*(k-1) is 0+2+...+(2k-2).
		"function sumEven(n) { const k = Math.floor(n / 2); return k > 0 ? k * (k - 1) : 0; }",
		// Off-by-one bound: odd floor is not the even count.
		"function sumEven(n) { const k = Math.floor((n - 1) / 2); return k * (k + 1); }",
		// Unguarded formula returns the empty sum for n < 2 as 0 anyway, but
		// without the guard this exact shape is not proven.
		"function sumEven(n) { const k = Math.floor(n / 2); return k * (k + 1); }",
		// Executes nothing provable: calls out to another helper.
		"function sumEven(n) { return helper(n); }",
	} {
		passed, reason := proveEvenSum(reply)
		require.Nil(t, passed, reply)
		require.Equal(t, "code_not_in_proven_subset", reason)
	}
}
