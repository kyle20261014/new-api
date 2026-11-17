package smartrouting

import (
	"math"
	"reflect"
	"testing"
	"time"
)

var rankTestNow = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

func rankTestCandidate(group string, cost float64, n, success, samples int64, mean float64) RankCandidate {
	return RankCandidate{
		Group: group, Comparable: true, Cost: cost,
		Observations: []RankObservation{{
			Key: RankKey{"model", group, "sync", "provider-v1", "chat"}, Stage: "attempt",
			Eligible: n, Success: success, SpeedSamples: samples, MeanMS: mean,
			LastObservation: rankTestNow, ActiveKnown: true,
		}},
	}
}

func rankTestRun(t *testing.T, strategy string, candidates ...RankCandidate) RankResult {
	t.Helper()
	result, err := Rank(RankRequest{Strategy: strategy, ClientModel: "model", RequestID: "server-seed", Attempt: 7, Now: rankTestNow, Candidates: candidates})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func rankTestGroups(result RankResult) []string {
	groups := make([]string, len(result.Candidates))
	for i, row := range result.Candidates {
		groups[i] = row.Group
	}
	return groups
}

func rankTestOrder(t *testing.T, result RankResult, want ...string) {
	t.Helper()
	if got := rankTestGroups(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v; want %v", got, want)
	}
}

func TestRankPriceAndColdStart(t *testing.T) {
	a := rankTestCandidate("a", 3, 0, 0, 0, 0)
	b := rankTestCandidate("b", 0, 0, 0, 0, 0)
	c := rankTestCandidate("c", 1, 0, 0, 0, 0)
	c.Comparable = false
	for _, strategy := range []string{"balanced", "price", "speed", "success"} {
		t.Run(strategy, func(t *testing.T) {
			result := rankTestRun(t, strategy, c, a, b)
			rankTestOrder(t, result, "b", "a", "c")
			if strategy == "balanced" && result.FallbackReason != "cold_start" {
				t.Fatalf("cold fallback = %q", result.FallbackReason)
			}
			if result.Candidates[0].P != 1 || result.Candidates[1].P != 0 || result.Candidates[2].P != .5 {
				t.Fatalf("price normalization: %+v", result.Candidates)
			}
		})
	}
	// Unknown comparisons use IDs even if their metrics differ dramatically.
	a.Comparable, b.Comparable = false, false
	a.Observations[0].Eligible, a.Observations[0].Success = 20, 0
	b.Observations[0].Eligible, b.Observations[0].Success = 20, 20
	result := rankTestRun(t, "price", b, c, a)
	rankTestOrder(t, result, "a", "b", "c")
	if result.FallbackReason != "price_unavailable" {
		t.Fatalf("all unavailable reason = %q", result.FallbackReason)
	}
	// Manual retains the saved order, regardless of metrics or price.
	rankTestOrder(t, rankTestRun(t, "manual", c, b, a), "c", "b", "a")
	// A single comparable or a set of identical prices always has P=1.
	for _, candidates := range [][]RankCandidate{{b}, {a, b}} {
		for i := range candidates {
			candidates[i].Comparable, candidates[i].Cost = true, 0
		}
		for _, row := range rankTestRun(t, "price", candidates...).Candidates {
			if row.P != 1 {
				t.Fatalf("equal/zero price P=%v", row.P)
			}
		}
	}
}

func TestRankTrustedSamplesFreshnessAndTies(t *testing.T) {
	unknown := rankTestCandidate("cheap", 0, 19, 19, 4, 1)
	known := rankTestCandidate("known", 10, 20, 5, 5, 100)
	for _, strategy := range []string{"success", "speed"} {
		rankTestOrder(t, rankTestRun(t, strategy, unknown, known), "known", "cheap")
	}
	known.Observations[0].LastObservation = rankTestNow.Add(-10 * time.Minute)
	if !rankTestRun(t, "success", known).Candidates[0].ReliabilityTrusted {
		t.Fatal("exactly 10min should be fresh")
	}
	known.Observations[0].LastObservation = known.Observations[0].LastObservation.Add(-time.Nanosecond)
	result := rankTestRun(t, "balanced", known, unknown)
	rankTestOrder(t, result, "cheap", "known")
	if result.FallbackReason != "cold_start" || !result.Candidates[1].Stale || result.Candidates[1].R != .5 || result.Candidates[1].S != .5 {
		t.Fatalf("stale did not degrade: %+v", result)
	}
	if result.Candidates[1].Observations[0].Eligible != 20 {
		t.Fatal("stale display lost raw 24h observations")
	}
	a := rankTestCandidate("a", 2, 20, 18, 5, 200)
	b := rankTestCandidate("b", 2, 20, 19, 5, 300)
	c := rankTestCandidate("c", 2, 20, 19, 5, 100)
	rankTestOrder(t, rankTestRun(t, "price", a, b, c), "c", "b", "a")
	rankTestOrder(t, rankTestRun(t, "success", a, b, c), "c", "b", "a")
	rankTestOrder(t, rankTestRun(t, "speed", a, b, c), "c", "a", "b")
	a.Observations[0].MeanMS, b.Observations[0].MeanMS, c.Observations[0].MeanMS = 100, 100, 100
	a.Observations[0].Success, b.Observations[0].Success, c.Observations[0].Success = 19, 19, 19
	for _, strategy := range []string{"price", "speed", "success"} {
		result := rankTestRun(t, strategy, c, b, a)
		rankTestOrder(t, result, "a", "b", "c")
		if result.Candidates[0].S != 1 || result.Candidates[0].R != .95 {
			t.Fatalf("equal speed/Beta prior: %+v", result.Candidates[0])
		}
	}
}

func TestRankScoresAndLoad(t *testing.T) {
	a := rankTestCandidate("a", 1, 20, 19, 5, 100)
	b := rankTestCandidate("b", 3, 20, 9, 5, 300)
	a.Observations[0].Active, a.Observations[0].Sent60s, a.Observations[0].Limited60s = 3, 10, 2
	result := rankTestRun(t, "balanced", b, a)
	rankTestOrder(t, result, "a", "b")
	row := result.Candidates[0]
	want := .20 + .40*.95 + .25 + .15*.2
	if math.Abs(row.Score-want) > 1e-15 || row.L != .2 || row.Rate429 != .2 {
		t.Fatalf("score=%v L=%v rate=%v want=%v", row.Score, row.L, row.Rate429, want)
	}
	// Known idle/no sends is L=1; unavailable leases are L=.5, never idle.
	if result.Candidates[1].L != 1 {
		t.Fatal("no 60s sends should yield zero 429 rate")
	}
	b.Observations[0].ActiveKnown = false
	row = rankTestRun(t, "price", b).Candidates[0]
	if row.LoadKnown || row.L != .5 {
		t.Fatalf("unknown load = %+v", row)
	}
}

func TestRankVariantFoldingAndAsyncStage(t *testing.T) {
	a := rankTestCandidate("a", 1, 100, 100, 10, 1)
	first := &a.Observations[0]
	first.Active, first.Sent60s, first.Limited60s = 2, 10, 1
	second := *first
	second.Key.ProviderKey, second.Active, second.Sent60s, second.Limited60s = "provider-v2", 3, 30, 9
	a.Observations = append(a.Observations, second)
	row := rankTestRun(t, "balanced", a).Candidates[0]
	if !row.MixedProviderOperation || row.ReliabilityTrusted || row.SpeedTrusted || row.R != .5 || row.S != .5 {
		t.Fatalf("mixed variant invented metrics: %+v", row)
	}
	if row.Active != 5 || row.Rate429 != .25 || row.L != .125 {
		t.Fatalf("load folding = active:%d rate:%v L:%v", row.Active, row.Rate429, row.L)
	}
	a.Observations[1].ActiveKnown = false
	if row = rankTestRun(t, "price", a).Candidates[0]; row.LoadKnown || row.L != .5 {
		t.Fatal("one unknown variant must make group load unknown")
	}
	// Submit acceptance must not masquerade as task success/speed or active.
	a = rankTestCandidate("a", 1, 100, 100, 100, 1)
	a.Observations[0].Key.ProtocolClass = "async"
	a.Observations[0].Active = 100
	terminal := a.Observations[0]
	terminal.Stage, terminal.Success, terminal.MeanMS, terminal.Active = "task_terminal", 50, 100000, 2
	terminal.SpeedSamples = 50
	terminal.Response1hSamples, terminal.Response1hMeanMS = 3, 90000
	a.Observations = append(a.Observations, terminal)
	row = rankTestRun(t, "success", a).Candidates[0]
	if row.MixedProviderOperation || row.R != float64(69)/120 || row.Active != 2 || rankMean(row) != 100000 {
		t.Fatalf("async stage mixed: %+v", row)
	}
	if row.Observations[1].Response1hSamples != 3 || row.Observations[0].Success != 100 {
		t.Fatal("per-stage display observations were lost")
	}
	// A second executable async variant with no terminal samples must still
	// make group R/S mixed and load unknown, rather than disappearing.
	missingTerminal := a.Observations[0]
	missingTerminal.Key.ProviderKey = "new-provider"
	a.Observations = append(a.Observations, missingTerminal)
	row = rankTestRun(t, "success", a).Candidates[0]
	if !row.MixedProviderOperation || row.ReliabilityTrusted || row.SpeedTrusted || row.LoadKnown {
		t.Fatalf("missing terminal variant silently ignored: %+v", row)
	}
}

func TestRankNonFiniteAndIntegerBoundaries(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		a := rankTestCandidate("a", value, 20, 20, 5, value)
		a.Observations[0].Response1hSamples, a.Observations[0].Response1hMeanMS = 1, value
		row := rankTestRun(t, "balanced", a).Candidates[0]
		if row.Comparable || row.SpeedTrusted || row.Cost != 0 || row.Observations[0].Response1hSamples != 0 {
			t.Fatalf("invalid value %v trusted: %+v", value, row)
		}
		for _, score := range []float64{row.P, row.R, row.S, row.L, row.Score} {
			if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
				t.Fatalf("invalid normalized score %v", score)
			}
		}
	}
	a := rankTestCandidate("a", 0, math.MaxInt64, math.MaxInt64, 5, 0)
	b := rankTestCandidate("b", math.MaxFloat64, 20, 20, 5, math.MaxFloat64)
	a.Observations[0].Active = math.MaxInt64
	result := rankTestRun(t, "price", b, a)
	rankTestOrder(t, result, "a", "b")
	if result.Candidates[0].P != 1 || result.Candidates[1].P != 0 || result.Candidates[0].S != 1 || result.Candidates[1].S != 0 {
		t.Fatalf("max float normalization failed: %+v", result)
	}
	if result.Candidates[0].R != 1 || result.Candidates[0].L <= 0 {
		t.Fatal("integer addition overflowed")
	}
	second := a.Observations[0]
	second.Key.ProviderKey, second.Active = "other", 1
	a.Observations = append(a.Observations, second)
	if rankTestRun(t, "price", a).Candidates[0].LoadKnown {
		t.Fatal("overflowing folded active must be unknown")
	}
	for _, counts := range [][2]int64{{-1, 0}, {20, -1}, {20, 21}} {
		a = rankTestCandidate("a", 0, counts[0], counts[1], 0, 0)
		if rankTestRun(t, "success", a).Candidates[0].ReliabilityTrusted {
			t.Fatal("invalid sample counts trusted")
		}
	}
}

func TestRankHashGoldenTopBandAndNoMutation(t *testing.T) {
	// Independent SHA256 fixture: uint64-BE lengths for
	// [smart-routing-v1,server-seed,7,model,a,b,组] gives
	// e93bd56d788cdc312323fc68a2190bcc8f9696dc26486392f3aaeb0a073c4fe5,
	// u=0.911069239832205. It selects the last of three equal weights.
	a := rankTestCandidate("a", 1, 20, 19, 5, 100)
	b := rankTestCandidate("b", 1, 20, 19, 5, 100)
	c := rankTestCandidate("组", 1, 20, 19, 5, 100)
	d := rankTestCandidate("outside", 1, 20, 0, 0, 0)
	for _, candidates := range [][]RankCandidate{{a, b, c, d}, {d, c, a, b}, {b, a, d, c}} {
		result := rankTestRun(t, "balanced", candidates...)
		rankTestOrder(t, result, "组", "a", "b", "outside")
		if result.Candidates[0].Score-result.Candidates[3].Score <= .02 {
			t.Fatal("fixture must exclude clearly worse candidate from topband")
		}
	}
	// Active is a second divisor on the topband selection weight, even when
	// scores happen to be equal. Unknown active uses denominator one.
	req := RankRequest{ClientModel: "model", RequestID: "server-seed", Attempt: 7}
	band := []RankedCandidate{{RankCandidate: a, Score: .8, LoadKnown: true}, {RankCandidate: b, Score: .8, LoadKnown: true}, {RankCandidate: c, Score: .8, LoadKnown: true, Active: 100}}
	if chosen := rankChoose(req, band); chosen != "b" {
		t.Fatalf("weighted golden selected %q; want b", chosen)
	}
	band[2].LoadKnown = false
	if chosen := rankChoose(req, band); chosen != "组" {
		t.Fatalf("unknown denominator selected %q; want 组", chosen)
	}
	// Repeated calls don't reorder input, sanitize it in place, or alias output.
	a.Cost, a.Observations[0].MeanMS = math.Inf(1), math.Inf(1)
	input := []RankCandidate{b, a}
	result := rankTestRun(t, "price", input...)
	result.Candidates[0].Observations[0].Success = 0
	if input[0].Group != "b" || input[0].Observations[0].Success != 19 || !math.IsInf(input[1].Observations[0].MeanMS, 1) {
		t.Fatal("Rank mutated/aliased input")
	}
	// Different server seeds/attempts distribute only within the near topband.
	selected := make(map[string]bool)
	for attempt := uint64(0); attempt < 100; attempt++ {
		req := RankRequest{Strategy: "balanced", ClientModel: "model", RequestID: "server-seed", Attempt: attempt, Now: rankTestNow, Candidates: []RankCandidate{b, c, d}}
		got, err := Rank(req)
		if err != nil {
			t.Fatal(err)
		}
		selected[got.Candidates[0].Group] = true
	}
	if !selected["b"] || !selected["组"] || selected["outside"] {
		t.Fatalf("topband distribution: %v", selected)
	}
}

func TestRankTopBandIncludesPointZeroTwo(t *testing.T) {
	a := rankTestCandidate("a", 1, 20, 19, 5, 100)
	b := rankTestCandidate("b", 1, 20, 17, 5, 100)
	c := rankTestCandidate("c", 1, 20, 16, 5, 100)
	// With all other dimensions equal, two fewer successes out of n=20
	// change R by 2/40, hence the score by exactly .02; three change it by .03.
	selected := make(map[string]bool)
	for attempt := uint64(0); attempt < 100; attempt++ {
		result, err := Rank(RankRequest{Strategy: "balanced", ClientModel: "model", RequestID: "server-seed", Attempt: attempt, Now: rankTestNow, Candidates: []RankCandidate{a, b, c}})
		if err != nil {
			t.Fatal(err)
		}
		selected[result.Candidates[0].Group] = true
	}
	if !selected["a"] || !selected["b"] || selected["c"] {
		t.Fatalf("inclusive .02 boundary: %v", selected)
	}
}

func TestRankRejectsInvalidIdentityAndStrategy(t *testing.T) {
	base := RankRequest{Strategy: "price", ClientModel: "model", Now: rankTestNow, Candidates: []RankCandidate{rankTestCandidate("a", 1, 20, 20, 5, 100)}}
	for _, mutate := range []func(*RankRequest){
		func(r *RankRequest) { r.Strategy = "custom" },
		func(r *RankRequest) { r.Strategy = "balanced" },
		func(r *RankRequest) { r.Now = time.Time{} },
		func(r *RankRequest) { r.ClientModel = "" },
		func(r *RankRequest) { r.Candidates = append(r.Candidates, r.Candidates[0]) },
		func(r *RankRequest) { r.Candidates[0].Group = "" },
		func(r *RankRequest) { r.Candidates[0].Observations[0].Key.ClientModel = "other" },
		func(r *RankRequest) { r.Candidates[0].Observations[0].Key.ProtocolClass = "bad" },
		func(r *RankRequest) { r.Candidates[0].Observations[0].Stage = "bad" },
		func(r *RankRequest) {
			r.Candidates[0].Observations = append(r.Candidates[0].Observations, r.Candidates[0].Observations[0])
		},
	} {
		r := base
		r.Candidates = append([]RankCandidate(nil), base.Candidates...)
		r.Candidates[0].Observations = append([]RankObservation(nil), base.Candidates[0].Observations...)
		mutate(&r)
		if _, err := Rank(r); err == nil {
			t.Fatalf("invalid request accepted: %+v", r)
		}
	}
	if result := rankTestRun(t, "price"); len(result.Candidates) != 0 {
		t.Fatal("empty input should return an empty ranking")
	}
}
