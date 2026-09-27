package model

const (
	ServiceTypeTCP   = "tcp"
	ServiceTypeUDP   = "udp"
	ServiceTypeHTTP  = "http"
	ServiceTypeHTTPS = "https"
	ServiceTypeVNC   = "vnc"
	ServiceTypeVPN   = "vpn"
	ServiceTypeRDP   = "rdp"
	ServiceTypeSSH   = "ssh"
	ServiceTypeRTSP  = "rtsp"
)

type Service struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Hostname    string `json:"hostname"`
	Type        string `json:"type"`
	Port        int    `json:"port"`
}
