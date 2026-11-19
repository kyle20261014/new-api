package lmfpd

import "errors"

const detectorSchema = "shared-detector-v2"

type bankModel struct {
	ID            string  `json:"id"`
	DisplayName   string  `json:"display_name"`
	Family        string  `json:"family"`
	FamilyName    string  `json:"family_name"`
	ResponseCount float64 `json:"response_count"`
}

// bank holds the fields scoring reads. bundle.json repeats them at its top level, so it parses as a bank too.
type bank struct {
	BuiltAt string      `json:"built_at"`
	Models  []bankModel `json:"models"`
}

func newDetector(b *bank, det *detector) (*Detector, error) {
	matches := b.Models != nil && det.schema == detectorSchema && b.BuiltAt == det.bankBuiltAt &&
		len(b.Models) == len(det.modelIDs) && len(det.responseCounts) == len(det.modelIDs)
	for i := 0; matches && i < len(b.Models); i++ {
		matches = b.Models[i].ID == det.modelIDs[i] && b.Models[i].ResponseCount == det.responseCounts[i]
	}
	if !matches {
		return nil, errors.New("The reference bank does not match its detector.")
	}
	return &Detector{bank: b, detector: det}, nil
}
