package services

import (
	"fmt"
	"net"
	"time"

	"foilen-realm/model"
)

const scanDialTimeout = 300 * time.Millisecond

type knownPort struct {
	port int
	name string
	typ  string
}

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
