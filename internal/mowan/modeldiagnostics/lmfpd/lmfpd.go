// Package lmfpd is the offline scoring subset of Ikaleio/lm-detector at 70159b7536a2ceb55c478c8c9472da81d3627e59.
// See LICENSE. Network sampling and download entry points are intentionally omitted.
package lmfpd

import "errors"

// Output is one model reply and the number count its challenge asked for.
type Output struct {
	Text          string `json:"text"`
	ExpectedCount int    `json:"expected_count"`
}

// Challenge is one prompt sent to the model.
type Challenge struct {
	ID            string `json:"id"`
	ExpectedCount int    `json:"expected_count"`
	Prompt        string `json:"prompt"`
}

// AnalysisResult is one candidate model.
type AnalysisResult struct {
	Model       string   `json:"model"`
	DisplayName string   `json:"display_name"`
	Family      string   `json:"family"`
	FamilyName  string   `json:"family_name"`
	Score       float64  `json:"score"`
	Probability *float64 `json:"probability"`
}

// Diagnostic describes how one output was parsed.
type Diagnostic struct {
	Index              int  `json:"index"`
	ParsedNumbers      int  `json:"parsed_numbers"`
	MinimumNumbers     int  `json:"minimum_numbers"`
	Accepted           bool `json:"accepted"`
	RawOutOfRangeCount int  `json:"raw_out_of_range_count"`
}

// Sequence summarizes how consecutive numbers change across the accepted outputs.
type Sequence struct {
	Autocorrelation float64 `json:"autocorrelation"`
	SmallStepShare  float64 `json:"small_step_share"`
	ModelLike       bool    `json:"model_like"`
}

// Calibration identifies the temperature calibration behind the probabilities.
type Calibration struct {
	Method string  `json:"method"`
	Run    string  `json:"run"`
	Tau    float64 `json:"tau"`
	SHA256 *string `json:"sha256"`
}

// ModelVersion identifies the detector.
type ModelVersion struct {
	BaseSHA256 string `json:"base_sha256"`
}

// Evidence explains the decision.
type Evidence struct {
	Insufficient bool   `json:"insufficient"`
	Label        string `json:"label"`
	Reason       string `json:"reason"`
	// Threshold is always null.
	Threshold *float64 `json:"threshold"`
	Method    string   `json:"method"`
}

// Analysis is the scoring result. Every field is always present, so the JSON shape matches the other SDKs.
type Analysis struct {
	// Decision is scored, partial, unscorable, or not_model.
	Decision             string `json:"decision"`
	Prediction           string `json:"prediction"`
	PredictionName       string `json:"prediction_name"`
	FamilyPrediction     string `json:"family_prediction"`
	FamilyPredictionName string `json:"family_prediction_name"`
	// Results lists candidates by ranking score, best first. Empty unless the decision is scored or partial.
	Results      []AnalysisResult `json:"results"`
	RankingScore *float64         `json:"ranking_score"`
	// Probability is the closed-set probability of the top candidate within the reference bank.
	Probability       *float64     `json:"probability"`
	ProbabilityStatus string       `json:"probability_status"`
	ProbabilityScope  *string      `json:"probability_scope"`
	Calibration       *Calibration `json:"calibration"`
	UsedOutputs       int          `json:"used_outputs"`
	Diagnostics       []Diagnostic `json:"diagnostics"`
	Sequence          *Sequence    `json:"sequence"`
	Method            string       `json:"method"`
	ModelVersion      ModelVersion `json:"model_version"`
	Evidence          Evidence     `json:"evidence"`
}

// Detector holds a loaded reference bank and detector. It is safe for concurrent use.
type Detector struct {
	bank     *bank
	detector *detector
}

// BuiltAt is the built_at of the reference bank, which identifies the data build.
func (d *Detector) BuiltAt() string { return d.bank.BuiltAt }

// Analyze scores one to three saved replies like `fpd --input`. Three valid replies give a calibrated ranking;
// fewer give a partial ranking unless strict is set.
func (d *Detector) Analyze(outputs []Output, strict bool) (*Analysis, error) {
	if len(outputs) < 1 || len(outputs) > 3 {
		return nil, errInvalidOutputs
	}
	prepared := make([]Output, len(outputs))
	for i, output := range outputs {
		if output.ExpectedCount <= 0 || output.ExpectedCount > maxSafeInteger {
			return nil, errInvalidOutputs
		}
		numbers := ParseNumbers(output.Text)
		text := output.Text
		if !strict && len(numbers) > output.ExpectedCount {
			text = joinNumbers(numbers[:output.ExpectedCount])
		}
		if len(numbers) < minimumNumbers(output.ExpectedCount) {
			text = ""
		}
		prepared[i] = Output{Text: text, ExpectedCount: output.ExpectedCount}
	}
	return d.score(prepared, strict)
}

// GenerateChallenges returns count random challenges, 1 to 3, in one of five languages, each asking for a distinct
// count from 292 to 332.
func GenerateChallenges(count int) ([]Challenge, error) {
	if count < 1 || count > 3 {
		return nil, errors.New("count must be an integer from 1 to 3.")
	}
	return generateChallenges(count), nil
}

func ptr[T any](v T) *T { return &v }
