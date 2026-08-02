package services

import (
	"fmt"
	"net"
	"time"

	"foilen-realm/model"
)

const scanDialTimeout = 300 * time.Millisecond

// knownPort is one entry of the fixed table ScanLocalPorts checks.
type knownPort struct {
	port int
	name string
	typ  string
}

// knownPorts is checked on 127.0.0.1 by ScanLocalPorts. UDP entries can't be
// confirmed by a TCP dial, so they're always reported unverified.
var knownPorts = []knownPort{
	{80, "http", model.ServiceTypeHTTP},
	{443, "https", model.ServiceTypeHTTPS},
	{32400, "plex", model.ServiceTypeHTTP},
	{22, "ssh", model.ServiceTypeSSH},
	{3389, "rdp", model.ServiceTypeRDP},
	{5900, "vnc", model.ServiceTypeVNC},
	{1194, "openvpn", model.ServiceTypeVPN},
	{11434, "ollama", model.ServiceTypeHTTP},
	{139, "netbios/samba", model.ServiceTypeTCP},
}

// ScanLocalPorts probes 127.0.0.1 for each of knownPorts, so the owner can
// quickly register the services this machine offers. Never touches
// libp2p/permissions.
func (f *Feature) ScanLocalPorts() []ScanResult {
	results := make([]ScanResult, 0, len(knownPorts))
	for _, kp := range knownPorts {
		if kp.typ == model.ServiceTypeUDP || kp.typ == model.ServiceTypeVPN {
			results = append(results, ScanResult{Port: kp.port, Open: false, GuessedName: kp.name, GuessedType: kp.typ, Unverifiable: true})
			continue
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", kp.port), scanDialTimeout)
		open := err == nil
		if open {
			conn.Close()
		}
		results = append(results, ScanResult{Port: kp.port, Open: open, GuessedName: kp.name, GuessedType: kp.typ})
	}
	return results
}
