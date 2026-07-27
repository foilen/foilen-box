package model

// Service type values for Service.Type.
const (
	ServiceTypeTCP   = "tcp"
	ServiceTypeUDP   = "udp"
	ServiceTypeHTTP  = "http"
	ServiceTypeHTTPS = "https"
	ServiceTypeVNC   = "vnc"
	ServiceTypeVPN   = "vpn"
	ServiceTypeRDP   = "rdp"
	ServiceTypeSSH   = "ssh"
)

// Service is a named local service a peer offers to proxy to, on request
// from other peers/groups it has granted ActionConnect to.
type Service struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hostname    string `json:"hostname"`
	Type        string `json:"type"`
	Port        int    `json:"port"`
}
