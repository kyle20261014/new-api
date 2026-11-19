package modeldiagnostics

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/internal/mowan/modeldiagnostics/lmfpd"
)

const FingerprintVersion = "lm-detector-70159b7536a2ceb55c478c8c9472da81d3627e59"
const BankSHA256 = "81d96a68404e2c394731d482f0e261f02b1a9c12e3bcfb4ccb677f8f0c715048"
const DetectorSHA256 = "a55d4bb275b09b630d9deeeac5a119077869a40dc65132ffff0ab87e58b7c9e9"

// Frozen official data from the same MIT-licensed upstream commit as the SDK.
//
//go:embed data/unified_bank.json
var bankBytes []byte

//go:embed data/shared_detector.bin
var detectorBytes []byte

var loadDetector = sync.OnceValues(func() (*lmfpd.Detector, error) {
	return verifiedDetector(bankBytes, detectorBytes)
})

func verifiedDetector(bank, detector []byte) (*lmfpd.Detector, error) {
	if fmt.Sprintf("%x", sha256.Sum256(bank)) != BankSHA256 {
		return nil, fmt.Errorf("reference bank SHA-256 mismatch")
	}
	if fmt.Sprintf("%x", sha256.Sum256(detector)) != DetectorSHA256 {
		return nil, fmt.Errorf("detector SHA-256 mismatch")
	}
	return lmfpd.LoadBytes(bank, detector)
}

func fingerprintPlan() (Plan, error) {
	p := Plan{Version: FingerprintVersion}
	if _, err := loadDetector(); err != nil {
		return p, fmt.Errorf("load frozen fingerprint references: %w", err)
	}
	cs, err := lmfpd.GenerateChallenges(3)
	if err != nil {
		return p, err
	}
	for _, c := range cs {
		p.Steps = append(p.Steps, Probe{ID: c.ID, Title: "指纹采样", Prompt: c.Prompt, MaxTokens: 4096, ExpectedCount: c.ExpectedCount})
	}
	return p, nil
}

type FingerprintDetails struct {
	Samples        []Answer        `json:"samples"`
	ValidSamples   int             `json:"valid_samples"`
	Analysis       *lmfpd.Analysis `json:"analysis"`
	BankVersion    string          `json:"bank_version"`
	BankSHA256     string          `json:"bank_sha256"`
	DetectorSHA256 string          `json:"detector_sha256"`
	Limitation     string          `json:"limitation"`
}

func evaluateFingerprint(answers []Answer) Result {
	d := FingerprintDetails{Samples: answers, BankSHA256: BankSHA256, DetectorSHA256: DetectorSHA256, Limitation: "仅表示参考库内相似性，不证明模型身份，也不能排除库外模型；本次选路结果不代表分组内全部渠道。"}
	r := Result{Status: "incomplete", Reasons: []string{}, Version: FingerprintVersion}
	det, err := loadDetector()
	if err != nil {
		r.Status = "unknown"
		r.Reasons = []string{"reference_data_unavailable"}
		r.Details = d
		return r
	}
	d.BankVersion = det.BuiltAt()
	outputs := []lmfpd.Output{}
	source := ""
	mixed := false
	sourceUnknown := false
	for _, a := range answers {
		if !a.Complete || strings.TrimSpace(a.Text) == "" || a.ExpectedCount < 292 || a.ExpectedCount > 332 {
			continue
		}
		outputs = append(outputs, lmfpd.Output{Text: a.Text, ExpectedCount: a.ExpectedCount})
		if a.Source == "" {
			sourceUnknown = true
		} else if source == "" {
			source = a.Source
		} else if source != a.Source {
			mixed = true
		}
	}
	if len(outputs) > 0 && len(outputs) <= 3 {
		analysis, e := det.Analyze(outputs, true)
		if e == nil {
			d.ValidSamples = analysis.UsedOutputs
			d.Analysis = analysis
		} else {
			r.Reasons = append(r.Reasons, "analysis_failed")
		}
	}
	if mixed || sourceUnknown {
		r.Status = "unknown"
		r.Reasons = []string{"sample_sources_mixed_or_unknown"}
		d.Analysis = nil
	} else if d.ValidSamples != 3 || len(answers) != 3 {
		r.Reasons = append(r.Reasons, "three_valid_samples_required")
		if d.Analysis != nil {
			d.Analysis.Results = nil
			d.Analysis.Probability = nil
		}
	} else if d.Analysis != nil && d.Analysis.Decision == "scored" {
		r.Status = "completed"
		r.Complete = true
	} else {
		r.Status = "unknown"
		r.Reasons = append(r.Reasons, "reference_similarity_unscorable")
	}
	r.Details = d
	return r
}
