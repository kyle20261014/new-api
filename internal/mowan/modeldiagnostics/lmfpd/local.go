package lmfpd

import "encoding/json"

// LoadBytes loads a previously verified, frozen reference bank without using
// Load's cache or network code. Analysis and decoding are the pinned upstream SDK.
func LoadBytes(bankBytes, detectorBytes []byte) (*Detector, error) {
	b := &bank{}
	if err := json.Unmarshal(bankBytes, b); err != nil {
		return nil, err
	}
	d, err := decodeDetector(detectorBytes)
	if err != nil {
		return nil, err
	}
	return newDetector(b, d)
}
