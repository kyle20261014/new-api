package smartrouting

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"
)

const RankAlgorithmVersion = "smart-routing-v1"

// RankKey is K. Stage remains separate, as required for asynchronous tasks.
type RankKey struct {
	ClientModel, BusinessGroup, ProtocolClass, ProviderKey, OperationKey string
}

// RankObservation is an already-folded snapshot of current hard-legal channels
// and nodes for exactly one K/stage. Eligible/Success cover 24h; speed fields
// cover 1h. MeanMS includes successful samples only, using the protocol's
// TTFT/complete-response/task-terminal duration. No historical filtering or DB
// access is performed here; the caller must supply the correct rolling windows.
type RankObservation struct {
	Key                         RankKey
	Stage                       string
	Eligible, Success           int64
	SpeedSamples                int64
	MeanMS                      float64
	Response1hSamples           int64
	Response1hMeanMS            float64
	LastObservation             time.Time
	ActiveKnown                 bool
	Active, Sent60s, Limited60s int64
}

// RankCandidate is one (client_model, business_group), never a physical channel.
// Comparable and Cost come from the existing quote resolver, in a common cost
// basis (effective ratio or estimated USD). Zero is a valid comparable cost.
// Represent every currently executable K, including zero-sample variants;
// omitting an executable variant would incorrectly imply a unique provider.
type RankCandidate struct {
	Group        string
	Comparable   bool
	Cost         float64
	Observations []RankObservation
}

type RankRequest struct {
	Strategy, ClientModel, RequestID string
	Attempt                          uint64
	Now                              time.Time
	Candidates                       []RankCandidate
}

// RankedCandidate exposes normalized scoring and the sanitized per-variant
// observations for display. Unknown values have explicit flags/reasons; their
// numeric zero values must not be displayed as measured zeroes.
type RankedCandidate struct {
	RankCandidate
	P, R, S, L, Score                        float64
	ReliabilityTrusted, SpeedTrusted         bool
	LoadKnown, MixedProviderOperation, Stale bool
	Active                                   int64
	Rate429                                  float64
	Reasons                                  []string
}

type RankResult struct {
	AlgorithmVersion string
	Strategy         string
	FallbackReason   string
	Candidates       []RankedCandidate
}

// Rank is pure: it neither mutates its input nor checks authorization/health.
// Input is the remaining hard-legal, health-tryable group set, in saved order
// for manual. The request ID must be server generated, never a client header.
func Rank(req RankRequest) (RankResult, error) {
	result := RankResult{AlgorithmVersion: RankAlgorithmVersion, Strategy: req.Strategy}
	switch req.Strategy {
	case "balanced", "price", "speed", "success", "manual":
	default:
		return result, fmt.Errorf("smart routing rank: unknown strategy %q", req.Strategy)
	}
	if req.Now.IsZero() || req.ClientModel == "" || !utf8.ValidString(req.ClientModel) {
		return result, fmt.Errorf("smart routing rank: nonzero now and valid client_model are required")
	}
	if req.Strategy == "balanced" && req.RequestID == "" {
		return result, fmt.Errorf("smart routing rank: balanced requires a server request ID")
	}
	groups := make(map[string]bool, len(req.Candidates))
	result.Candidates = make([]RankedCandidate, 0, len(req.Candidates))
	for _, candidate := range req.Candidates {
		if candidate.Group == "" || !utf8.ValidString(candidate.Group) || groups[candidate.Group] {
			return result, fmt.Errorf("smart routing rank: invalid or duplicate group %q", candidate.Group)
		}
		groups[candidate.Group] = true
		ranked, err := rankSnapshot(req, candidate)
		if err != nil {
			return result, err
		}
		result.Candidates = append(result.Candidates, ranked)
	}
	rows := result.Candidates
	minCost, maxCost, minMean, maxMean := math.Inf(1), 0.0, math.Inf(1), 0.0
	hasR, hasS := false, false
	for _, row := range rows {
		if row.Comparable {
			minCost, maxCost = math.Min(minCost, row.Cost), math.Max(maxCost, row.Cost)
		}
		if row.SpeedTrusted {
			minMean, maxMean = math.Min(minMean, row.S), math.Max(maxMean, row.S)
		}
		hasR, hasS = hasR || row.ReliabilityTrusted, hasS || row.SpeedTrusted
	}
	for i := range rows {
		if rows[i].Comparable {
			rows[i].P = rankNormalize(rows[i].Cost, minCost, maxCost)
		}
		if rows[i].SpeedTrusted {
			rows[i].S = rankNormalize(rows[i].S, minMean, maxMean)
		}
		rows[i].Score = .20*rows[i].P + .40*rows[i].R + .25*rows[i].S + .15*rows[i].L
	}
	strategy := req.Strategy
	if strategy == "balanced" && !hasR && !hasS {
		strategy, result.FallbackReason = "price", "cold_start"
	} else if strategy == "speed" && !hasS || strategy == "success" && !hasR {
		strategy, result.FallbackReason = "price", "insufficient_samples"
	}
	priceLess := func(a, b RankedCandidate) bool {
		if a.Comparable != b.Comparable {
			return a.Comparable
		}
		// Unavailable costs must not be ordered by unrelated numbers or metrics.
		if !a.Comparable {
			return a.Group < b.Group
		}
		if a.Cost != b.Cost {
			return a.Cost < b.Cost
		}
		if a.R != b.R {
			return a.R > b.R
		}
		if a.S != b.S {
			return a.S > b.S
		}
		return a.Group < b.Group
	}
	switch strategy {
	case "manual":
		return result, nil
	case "price":
		anyComparable := false
		for _, row := range rows {
			anyComparable = anyComparable || row.Comparable
		}
		if !anyComparable && result.FallbackReason == "" && len(rows) > 0 {
			result.FallbackReason = "price_unavailable"
		}
		sort.Slice(rows, func(i, j int) bool { return priceLess(rows[i], rows[j]) })
	case "success":
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			if a.ReliabilityTrusted != b.ReliabilityTrusted {
				return a.ReliabilityTrusted
			}
			if a.ReliabilityTrusted && a.R != b.R {
				return a.R > b.R
			}
			return priceLess(a, b)
		})
	case "speed":
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			if a.SpeedTrusted != b.SpeedTrusted {
				return a.SpeedTrusted
			}
			if a.SpeedTrusted && rankMean(a) != rankMean(b) {
				return rankMean(a) < rankMean(b)
			}
			if a.R != b.R {
				return a.R > b.R
			}
			return priceLess(a, b)
		})
	case "balanced":
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Score != rows[j].Score {
				return rows[i].Score > rows[j].Score
			}
			return rows[i].Group < rows[j].Group
		})
		if len(rows) > 1 {
			end := 1
			// Compare against the cutoff to keep the inclusive .02 boundary;
			// subtracting two rounded scores can turn .02 into .020000000000000018.
			for end < len(rows) && rows[end].Score >= rows[0].Score-.02 {
				end++
			}
			band := append([]RankedCandidate(nil), rows[:end]...)
			sort.Slice(band, func(i, j int) bool { return band[i].Group < band[j].Group })
			chosen := rankChoose(req, band)
			for i := range rows {
				if rows[i].Group == chosen {
					selected := rows[i]
					copy(rows[1:i+1], rows[:i])
					rows[0] = selected
					break
				}
			}
		}
	}
	return result, nil
}

func rankSnapshot(req RankRequest, candidate RankCandidate) (RankedCandidate, error) {
	row := RankedCandidate{RankCandidate: candidate, P: .5, R: .5, S: .5, L: .5}
	row.Observations = append([]RankObservation(nil), candidate.Observations...)
	if !candidate.Comparable || !rankNonnegative(candidate.Cost) {
		row.Comparable, row.Cost = false, 0
		row.Reasons = append(row.Reasons, "price_unavailable")
	}
	seen := make(map[struct {
		Key   RankKey
		Stage string
	}]bool)
	variants := make(map[RankKey]bool)
	indices := make([]int, 0, len(row.Observations))
	for i := range row.Observations {
		observation := &row.Observations[i]
		key := observation.Key
		if key.ClientModel != req.ClientModel || key.BusinessGroup != candidate.Group || key.ProviderKey == "" || key.OperationKey == "" {
			return row, fmt.Errorf("smart routing rank: group %q has mismatched or incomplete K %+v", candidate.Group, key)
		}
		switch key.ProtocolClass {
		case "sync", "stream", "async", "responses_ws", "realtime":
		default:
			return row, fmt.Errorf("smart routing rank: group %q has invalid protocol_class %q", candidate.Group, key.ProtocolClass)
		}
		if observation.Stage != "attempt" && observation.Stage != "task_terminal" {
			return row, fmt.Errorf("smart routing rank: group %q has invalid stage %q", candidate.Group, observation.Stage)
		}
		identity := struct {
			Key   RankKey
			Stage string
		}{key, observation.Stage}
		if seen[identity] {
			return row, fmt.Errorf("smart routing rank: group %q has duplicate K/stage %+v/%s", candidate.Group, key, observation.Stage)
		}
		seen[identity] = true
		variants[key] = true
		// Non-finite observations are unknown, never leaked into display JSON.
		if !rankNonnegative(observation.MeanMS) || observation.SpeedSamples < 0 {
			observation.MeanMS, observation.SpeedSamples = 0, 0
		}
		if !rankNonnegative(observation.Response1hMeanMS) || observation.Response1hSamples < 0 {
			observation.Response1hMeanMS, observation.Response1hSamples = 0, 0
		}
		if observation.Eligible < 0 || observation.Success < 0 || observation.Success > observation.Eligible {
			observation.Eligible, observation.Success = 0, 0
		}
		if observation.SpeedSamples > observation.Success {
			observation.MeanMS, observation.SpeedSamples = 0, 0
		}
		if key.ProtocolClass == "async" && observation.Stage == "task_terminal" || key.ProtocolClass != "async" && observation.Stage == "attempt" {
			indices = append(indices, i)
		}
	}
	row.MixedProviderOperation = len(variants) > 1
	if row.MixedProviderOperation {
		row.Reasons = append(row.Reasons, "mixed_provider_operation")
	} else if len(indices) == 1 {
		observation := row.Observations[indices[0]]
		fresh := !observation.LastObservation.IsZero() && !observation.LastObservation.After(req.Now) && req.Now.Sub(observation.LastObservation) <= 10*time.Minute
		row.Stale = !observation.LastObservation.IsZero() && req.Now.Sub(observation.LastObservation) > 10*time.Minute
		row.ReliabilityTrusted = fresh && observation.Eligible >= 20
		row.SpeedTrusted = fresh && observation.SpeedSamples >= 5
		if row.ReliabilityTrusted {
			row.R = (float64(observation.Success) + 19) / (float64(observation.Eligible) + 20)
		}
		if row.SpeedTrusted {
			row.S = observation.MeanMS // normalized after all group snapshots exist
		}
	}
	if !row.ReliabilityTrusted {
		row.Reasons = append(row.Reasons, "reliability_unknown")
	}
	if !row.SpeedTrusted {
		row.Reasons = append(row.Reasons, "speed_unknown")
	}
	if row.Stale {
		row.Reasons = append(row.Reasons, "stale")
	}
	row.LoadKnown = len(indices) > 0 && len(indices) == len(variants)
	var sent, limited float64
	for _, i := range indices {
		observation := row.Observations[i]
		if !observation.ActiveKnown || observation.Active < 0 || observation.Active > math.MaxInt64-row.Active || observation.Sent60s < 0 || observation.Limited60s < 0 || observation.Limited60s > observation.Sent60s {
			row.LoadKnown = false
			continue
		}
		row.Active += observation.Active
		sent += float64(observation.Sent60s)
		limited += float64(observation.Limited60s)
	}
	if row.LoadKnown {
		if sent > 0 {
			row.Rate429 = limited / sent
		}
		row.L = (1 / (1 + float64(row.Active))) * (1 - row.Rate429)
	} else {
		row.Active = 0
		row.Reasons = append(row.Reasons, "load_unknown")
	}
	return row, nil
}

func rankNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func rankNormalize(value, min, max float64) float64 {
	if min == max {
		return 1
	}
	return 1 - (value-min)/(max-min)
}

func rankMean(row RankedCandidate) float64 {
	for _, observation := range row.Observations {
		if observation.Key.ProtocolClass == "async" && observation.Stage == "task_terminal" || observation.Key.ProtocolClass != "async" && observation.Stage == "attempt" {
			return observation.MeanMS
		}
	}
	return 0
}

// Wire encoding is uint64 big-endian byte length followed by UTF-8 bytes for
// each field: version, server request ID, decimal attempt, model, then each
// group in UTF-8 byte order. Length-prefixing avoids concatenation collisions.
func rankChoose(req RankRequest, band []RankedCandidate) string {
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		hash.Write(length[:])
		hash.Write([]byte(value))
	}
	write(RankAlgorithmVersion)
	write(req.RequestID)
	write(strconv.FormatUint(req.Attempt, 10))
	write(req.ClientModel)
	for _, row := range band {
		write(row.Group)
	}
	digest := hash.Sum(nil)
	u := float64(binary.BigEndian.Uint64(digest[:8])>>11) / (1 << 53)
	weight := func(row RankedCandidate) float64 {
		denominator := 1.0
		if row.LoadKnown {
			denominator += float64(row.Active)
		}
		return math.Max(.01, row.Score) / denominator
	}
	var total, cumulative float64
	for _, row := range band {
		total += weight(row)
	}
	target := u * total
	for _, row := range band {
		cumulative += weight(row)
		if cumulative > target {
			return row.Group
		}
	}
	return band[len(band)-1].Group
}
