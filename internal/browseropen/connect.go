package browseropen

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OpenHTTP launches the OS default browser at the local proxy port, over
// plain http or https.
func OpenHTTP(port int, secure bool) error {
	scheme := "http"
	if secure {
		scheme = "https"
	}
	return Open(fmt.Sprintf("%s://127.0.0.1:%d", scheme, port))
}

// linuxTerminals are tried in order on linux/bsd; the first found on $PATH is used.
var linuxTerminals = []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xterm"}

// OpenSSH spawns a terminal running an ssh session against the local proxy
// port. Returns a plain error if no terminal emulator is found.
func OpenSSH(port int) error {
	sshArgs := []string{"-p", fmt.Sprintf("%d", port), "127.0.0.1"}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "Terminal" to do script "ssh -p %d 127.0.0.1"`, port)
		cmd = exec.Command("osascript", "-e", script)
	case "windows":
		args := append([]string{"/c", "start", "ssh"}, sshArgs...)
		cmd = exec.Command("cmd", args...)
	default:
		term := ""
		for _, candidate := range linuxTerminals {
			if _, err := exec.LookPath(candidate); err == nil {
				term = candidate
				break
			}
		}
		if term == "" {
			return fmt.Errorf("no terminal emulator found to open an ssh session")
		}
		args := append([]string{"-e", "ssh"}, sshArgs...)
		cmd = exec.Command(term, args...)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open ssh terminal: %w", err)
	}
	return nil
}

// OpenVNC writes a temporary .vnc connection file pointing at the local
// proxy port and opens it with the OS's registered VNC viewer, if any.
func OpenVNC(port int) error {
	content := fmt.Sprintf("[Connection]\nHost=127.0.0.1\nPort=%d\n", port)
	return openTempFile("foilen-box-*.vnc", content)
}

// OpenRDP writes a temporary .rdp connection file pointing at the local
// proxy port and opens it with the OS's registered RDP viewer, if any.
func OpenRDP(port int) error {
	content := fmt.Sprintf("full address:s:127.0.0.1:%d\n", port)
	return openTempFile("foilen-box-*.rdp", content)
}

// OpenOpenVPN writes a temporary .ovpn client config pointing at the local
// proxy port (the real VPN server is reached over the libp2p tunnel, over
// TCP) and opens it with the OS's registered OpenVPN handler, if any.
//
// excludeHosts are the real network addresses the underlying libp2p
// connection to the peer is using (see Engine.ConnectedHosts). Once
// redirect-gateway takes over the default route, those addresses must keep
// going through the original gateway, or the tunnel carrying the VPN
// traffic itself gets cut.
func OpenOpenVPN(port int, excludeHosts []string) error {
	var b strings.Builder
	b.WriteString("client\n")
	b.WriteString("dev tun\n")
	b.WriteString("proto tcp-client\n")
	fmt.Fprintf(&b, "remote 127.0.0.1 %d\n", port)
	b.WriteString("resolv-retry infinite\n")
	b.WriteString("nobind\n")
	b.WriteString("persist-key\n")
	b.WriteString("persist-tun\n")
	b.WriteString("redirect-gateway def1\n")
	for _, host := range excludeHosts {
		if strings.Contains(host, ":") {
			fmt.Fprintf(&b, "route-ipv6 %s/128 ::0\n", host)
		} else {
			fmt.Fprintf(&b, "route %s 255.255.255.255 net_gateway\n", host)
		}
	}
	b.WriteString("verb 3\n")
	return openTempFile("foilen-box-*.ovpn", b.String())
}

func openTempFile(pattern, content string) error {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return fmt.Errorf("failed to create temp connection file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write temp connection file: %w", err)
	}
	return Open(f.Name())
}
