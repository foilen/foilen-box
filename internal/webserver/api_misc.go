package webserver

import (
	"encoding/json"

	"foilen-box/internal/logging"
	"foilen-box/internal/spec"
	"foilen-box/internal/troubleshooting/dns"
	troubleshootingreport "foilen-box/internal/troubleshooting/report"
	"foilen-box/internal/troubleshooting/whois"
)

func handleSpecReport(a *api, _ json.RawMessage) (any, error) {
	return map[string]string{"text": spec.Report(a.configDir)}, nil
}

func handleLogsRead(a *api, params json.RawMessage) (any, error) {
	var p struct {
		Search string `json:"search"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	text, err := logging.ReadTail(a.logDir, p.Search)
	if err != nil {
		return nil, err
	}
	return map[string]string{"text": text}, nil
}

func handleTroubleshootingRun(_ *api, params json.RawMessage) (any, error) {
	var p struct {
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	return map[string]string{"text": runTroubleshoot(p.Domain)}, nil
}

func runTroubleshoot(domain string) string {
	whoisResp, whoisErr := whois.Query(domain)
	dnsEntries := dns.Query(domain)

	var prefix string
	if whoisErr != nil {
		prefix = "WHOIS ERROR: " + whoisErr.Error() + "\n\n"
		whoisResp = nil
	}
	return prefix + troubleshootingreport.Format(whoisResp, dnsEntries)
}
