package browseropen

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
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

// linuxTerminals is the list of terminal emulators tried, in order, on
// linux/bsd; the first one found on $PATH is used.
var linuxTerminals = []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xterm"}

// OpenSSH spawns a terminal running an ssh session against the local proxy
// port. Best-effort: on a system with no terminal emulator found (or where
// exec isn't usable, e.g. Android) it returns a plain error instead of
// panicking or failing the caller's whole request.
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
