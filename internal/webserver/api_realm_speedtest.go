package webserver

import (
	"encoding/json"
	"fmt"
)

// speedTestResult is the wire shape of one peer's speed test outcome.
type speedTestResult struct {
	PeerID       string  `json:"peerId"`
	DownloadMbps float64 `json:"downloadMbps"`
	UploadMbps   float64 `json:"uploadMbps"`
	Error        string  `json:"error,omitempty"`
}

func handleRealmRunSpeedTest(a *api, params json.RawMessage) (any, error) {
	var p struct {
		PeerId string `json:"peerId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.PeerId == "" {
		return nil, fmt.Errorf("please select a peer")
	}
	result := a.realmSpeedTest.RunSpeedTest(p.PeerId)
	return speedTestResult{
		PeerID:       result.PeerID,
		DownloadMbps: result.DownloadMbps,
		UploadMbps:   result.UploadMbps,
		Error:        result.Error,
	}, nil
}
